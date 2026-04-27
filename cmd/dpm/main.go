package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"dpm.fi/dpm/internal/catalog"
	"dpm.fi/dpm/internal/dotfiles"
	"dpm.fi/dpm/internal/engine"
	"dpm.fi/dpm/internal/profiles"
	"dpm.fi/dpm/internal/search"
	"dpm.fi/dpm/internal/serve"
	"dpm.fi/dpm/internal/settings"
	"github.com/urfave/cli/v2"
)

var version = "v0.5.2"

const (
	commandSummaryFormat = "   %-18s %s\n"
	printVersionLabel    = "Print version"
	dpmTUIBinaryName     = "dpm-tui"
	initEngineErrFormat  = "failed to initialize engine: %w"
	engineErrFormat      = "engine: %w"
)

func wrapInitEngineErr(err error) error {
	return fmt.Errorf(initEngineErrFormat, err)
}

func wrapEngineErr(err error) error {
	return fmt.Errorf(engineErrFormat, err)
}

// cliSpinner shows a terminal spinner while work runs in background.
type cliSpinner struct {
	msg    string
	frames []string
	stop   chan struct{}
	wg     sync.WaitGroup
}

type commandAlias struct {
	Short string
	Long  string
}

type optionSpec struct {
	Label string
	Usage string
}

var topCommandAliases = map[string]commandAlias{
	"install":  {Short: "i", Long: "install"},
	"remove":   {Short: "r", Long: "remove"},
	"update":   {Short: "u", Long: "update"},
	"list":     {Short: "l", Long: "list"},
	"search":   {Short: "s", Long: "search"},
	"inspect":  {Short: "x", Long: "inspect"},
	"verify":   {Short: "k", Long: "verify"},
	"version":  {Short: "v", Long: "version"},
	"apply":    {Short: "a", Long: "apply"},
	"config":   {Short: "c", Long: "config"},
	"settings": {Long: "settings"},
	"restore":  {Short: "o", Long: "restore"},
	"serve":    {Short: "n", Long: "serve"},
	"bubble":   {Short: "b", Long: "bubble"},
	"doctor":   {Short: "d", Long: "doctor"},
}

var topCommandOrder = []string{
	"install",
	"remove",
	"update",
	"list",
	"search",
	"inspect",
	"verify",
	"version",
	"apply",
	"config",
	"settings",
	"restore",
	"serve",
	"bubble",
	"doctor",
}

var configSubcommandAliases = map[string]commandAlias{
	"install": {Short: "i", Long: "install"},
	"inspect": {Short: "x", Long: "inspect"},
}

var settingsSubcommandAliases = map[string]commandAlias{
	"list":   {Long: "list"},
	"set":    {Long: "set"},
	"toggle": {Long: "toggle"},
	"reset":  {Long: "reset"},
}

func newSpinner(msg string) *cliSpinner {
	return &cliSpinner{
		msg:    msg,
		frames: []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"},
		stop:   make(chan struct{}),
	}
}

func (s *cliSpinner) Start() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		i := 0
		for {
			select {
			case <-s.stop:
				fmt.Printf("\r\033[K")
				return
			default:
				fmt.Printf("\r  %s %s", s.frames[i%len(s.frames)], s.msg)
				i++
				time.Sleep(100 * time.Millisecond)
			}
		}
	}()
}

func (s *cliSpinner) Update(msg string) {
	s.msg = msg
}

func (s *cliSpinner) Stop() {
	close(s.stop)
	s.wg.Wait()
}

// asciiLogo returns the ASCII art logo
func asciiLogo() string {
	return `/********************************** *****************************************************
 *                                                                                        
 *				   ░███████   ░█████████  ░███     ░███  dUmb
 *				  ░██   ░██  ░██     ░██ ░████   ░████    PAckET
 *  				 ░██    ░██ ░██     ░██ ░██░██ ░██░██      mAnAGEr
 *  				 ░██    ░██ ░█████████  ░██ ░████ ░██        bY
 *  				 ░██    ░██ ░██         ░██  ░██  ░██ 	       IlJa
 *  				 ░██   ░██  ░██         ░██       ░██ 	         rOBin
 *  				 ░███████   ░██         ░██       ░██              HeNrY
 *
 *  					░▒▓ dpm - Dumb Package Manager ▓▒░
 *  ────────────────────────────────────────────────────────────────────────────────────
 *  ` + version + `
 *  A package manager for everyone - Install tools and configs easily
 *  by Ilja Ylikangas, Robin Niinemets, Henry Isakoff
 *
 ****************************************************************************************/`
}

// printVersion prints version with commands
func printVersion(app *cli.App) {
	fmt.Println(asciiLogo())
	fmt.Println()
	printStyledAppHelp(app.Writer, app)
}

func aliasLabel(a commandAlias, fallbackLong string) string {
	short := ""
	long := fallbackLong
	if a.Short != "" {
		short = "-" + a.Short
	}
	if a.Long != "" {
		long = a.Long
	}
	if short != "" {
		return fmt.Sprintf("%s, --%s", short, long)
	}
	return fmt.Sprintf("--%s", long)
}

func topCommandAliasLabel(name string) string {
	if a, ok := topCommandAliases[name]; ok {
		return aliasLabel(a, name)
	}
	return aliasLabel(commandAlias{}, name)
}

func subcommandAliasLabel(parent, name string) string {
	if parent == "config" {
		if a, ok := configSubcommandAliases[name]; ok {
			return aliasLabel(a, name)
		}
	}
	if parent == "settings" {
		if a, ok := settingsSubcommandAliases[name]; ok {
			return aliasLabel(a, name)
		}
	}
	return aliasLabel(commandAlias{}, name)
}

func commandAliasLabel(cmd *cli.Command) string {
	parts := strings.Fields(cmd.HelpName)
	if len(parts) >= 3 {
		parent := parts[len(parts)-2]
		return subcommandAliasLabel(parent, cmd.Name)
	}
	return topCommandAliasLabel(cmd.Name)
}

func splitFlagNames(names []string) (string, string) {
	short := ""
	long := ""
	for _, n := range names {
		if len(n) == 1 && short == "" {
			short = "-" + n
			continue
		}
		if len(n) > 1 && long == "" {
			long = "--" + n
		}
	}
	return short, long
}

func isHelpOption(short, long string) bool {
	return long == "--help" || short == "-h"
}

func takesOptionValue(f cli.Flag) bool {
	takesValue, ok := f.(interface{ TakesValue() bool })
	return ok && takesValue.TakesValue()
}

func optionLabel(short, long string, takesValue bool) string {
	label := short
	if short == "" {
		label = long
	}
	if short != "" && long != "" {
		label = short + ", " + long
	}
	if takesValue {
		label += " <value>"
	}
	return label
}

func optionUsage(f cli.Flag) string {
	if withUsage, ok := f.(interface{ GetUsage() string }); ok {
		if usage := withUsage.GetUsage(); usage != "" {
			return usage
		}
	}
	return f.String()
}

func optionSpecs(flags []cli.Flag) []optionSpec {
	out := make([]optionSpec, 0, len(flags))
	for _, f := range flags {
		names := f.Names()
		if len(names) == 0 {
			continue
		}

		short, long := splitFlagNames(names)
		if isHelpOption(short, long) {
			continue
		}

		out = append(out, optionSpec{
			Label: optionLabel(short, long, takesOptionValue(f)),
			Usage: optionUsage(f),
		})
	}
	return out
}

func writeMultilineIndented(w io.Writer, text, indent string) {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			fmt.Fprintln(w)
			continue
		}
		fmt.Fprintf(w, "%s%s\n", indent, line)
	}
}

func printStyledAppHelp(w io.Writer, app *cli.App) {
	fmt.Fprintln(w, "USAGE:")
	fmt.Fprintf(w, "   %s <command> [command options]\n\n", app.Name)

	fmt.Fprintln(w, "COMMANDS:")

	cmdByName := make(map[string]*cli.Command, len(app.VisibleCommands()))
	for _, cmd := range app.VisibleCommands() {
		cmdByName[cmd.Name] = cmd
	}

	for _, name := range topCommandOrder {
		cmd := cmdByName[name]
		if cmd == nil {
			continue
		}
		fmt.Fprintf(w, commandSummaryFormat, topCommandAliasLabel(cmd.Name), cmd.Usage)
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "GLOBAL OPTIONS:")
	fmt.Fprintf(w, commandSummaryFormat, "-h, --help", "Show help")
	fmt.Fprintf(w, commandSummaryFormat, "-v, --version", printVersionLabel)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "TIP:")
	fmt.Fprintf(w, "   %s help <command> to see command-specific options.\n", app.Name)
}

func printStyledCommandHelp(w io.Writer, cmd *cli.Command) {
	fmt.Fprintln(w, "NAME:")
	fmt.Fprintf(w, "   %s - %s\n\n", cmd.HelpName, cmd.Usage)

	fmt.Fprintln(w, "ALIASES:")
	fmt.Fprintf(w, "   %s\n\n", commandAliasLabel(cmd))

	usageText := cmd.UsageText
	if usageText == "" {
		usageText = cmd.HelpName
	}
	fmt.Fprintln(w, "USAGE:")
	fmt.Fprintf(w, "   %s\n", usageText)

	if strings.TrimSpace(cmd.Description) != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "DESCRIPTION:")
		writeMultilineIndented(w, cmd.Description, "   ")
	}

	opts := optionSpecs(cmd.Flags)
	if len(opts) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "OPTIONS:")
		for _, opt := range opts {
			fmt.Fprintf(w, "   %-24s %s\n", opt.Label, opt.Usage)
		}
	}

	if len(cmd.VisibleCommands()) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "SUBCOMMANDS:")
		for _, sub := range cmd.VisibleCommands() {
			if sub.Name == "help" {
				continue
			}
			fmt.Fprintf(w, commandSummaryFormat, subcommandAliasLabel(cmd.Name, sub.Name), sub.Usage)
		}
		fmt.Fprintln(w)
		fmt.Fprintf(w, "   %s %s <subcommand> --help to see subcommand options.\n", strings.Fields(cmd.HelpName)[0], cmd.Name)
	}
}

func configureHelpPrinter() {
	cli.HelpPrinter = func(w io.Writer, templ string, data interface{}) {
		switch v := data.(type) {
		case *cli.App:
			printStyledAppHelp(w, v)
		case *cli.Command:
			printStyledCommandHelp(w, v)
		default:
			cli.HelpPrinterCustom(w, templ, data, nil)
		}
	}
}

func resolveCommandAliasToken(token string, aliasMap map[string]commandAlias) (string, bool) {
	for name, a := range aliasMap {
		if a.Short != "" && token == "-"+a.Short {
			return name, true
		}
		if a.Long != "" && token == "--"+a.Long {
			return name, true
		}
	}
	return "", false
}

func rewriteAliasArgs(args []string) []string {
	if len(args) < 2 {
		return args
	}
	out := append([]string(nil), args...)

	if resolved, ok := resolveCommandAliasToken(out[1], topCommandAliases); ok {
		out[1] = resolved
	}

	if len(out) >= 3 && out[1] == "config" {
		if resolved, ok := resolveCommandAliasToken(out[2], configSubcommandAliases); ok {
			out[2] = resolved
		}
	}

	if len(out) >= 3 && out[1] == "settings" {
		if resolved, ok := resolveCommandAliasToken(out[2], settingsSubcommandAliases); ok {
			out[2] = resolved
		}
	}

	if len(out) >= 3 && out[1] == "help" {
		if resolved, ok := resolveCommandAliasToken(out[2], topCommandAliases); ok {
			out[2] = resolved
		}
	}

	if len(out) >= 4 && out[1] == "help" && out[2] == "config" {
		if resolved, ok := resolveCommandAliasToken(out[3], configSubcommandAliases); ok {
			out[3] = resolved
		}
	}

	if len(out) >= 4 && out[1] == "help" && out[2] == "settings" {
		if resolved, ok := resolveCommandAliasToken(out[3], settingsSubcommandAliases); ok {
			out[3] = resolved
		}
	}

	return out
}

func enableShortOptionHandling(commands []*cli.Command) {
	for _, cmd := range commands {
		cmd.UseShortOptionHandling = true
		enableShortOptionHandling(cmd.Subcommands)
	}
}

// sha256File computes the hex-encoded SHA-256 digest of the file at path.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ----------------------------------------------------------------------------
// Last-search selection helpers
// ----------------------------------------------------------------------------

type lastSearchItem struct {
	N    int    `json:"n"`
	Type string `json:"type"` // "tool", "profile", "bundle"
	ID   string `json:"id"`   // tool ID or profile ID
	URL  string `json:"url"`  // for bundles
	Name string `json:"name"` // display name for bundles
}

type lastSearch struct {
	Query string           `json:"query"`
	Items []lastSearchItem `json:"items"`
}

func lastSearchPath(dpmDir string) string {
	return filepath.Join(dpmDir, "last-search.json")
}

func saveLastSearch(dpmDir string, ls lastSearch) {
	data, err := json.Marshal(ls)
	if err != nil {
		return
	}
	_ = os.WriteFile(lastSearchPath(dpmDir), data, 0o644)
}

// resolveArg resolves a numeric argument (from a previous dpm search) to the
// actual tool ID, profile ID, or bundle URL. Non-numeric arguments pass through
// unchanged with an empty itemType.
func resolveArg(dpmDir, arg string) (resolved, itemType string, err error) {
	n, convErr := strconv.Atoi(arg)
	if convErr != nil {
		return arg, "", nil // not a number — unchanged
	}
	data, readErr := os.ReadFile(lastSearchPath(dpmDir))
	if readErr != nil {
		return "", "", fmt.Errorf("no recent search — run 'dpm search <query>' first")
	}
	var ls lastSearch
	if jsonErr := json.Unmarshal(data, &ls); jsonErr != nil {
		return "", "", fmt.Errorf("corrupted last-search file — run 'dpm search' again")
	}
	for _, item := range ls.Items {
		if item.N == n {
			switch item.Type {
			case "tool", "profile":
				return item.ID, item.Type, nil
			case "bundle":
				return item.URL, "bundle", nil
			}
		}
	}
	return "", "", fmt.Errorf("no item [%d] in last search — run 'dpm search' again", n)
}

// ----------------------------------------------------------------------------
// runInspect is the shared implementation for `dpm inspect` and
// `dpm config inspect`. arg is the URL or last-search number; filename is
// an optional dotfile to view (empty = show the full bundle summary).
// ----------------------------------------------------------------------------
func resolveInspectRepoURL(arg, dpmDir string) (string, error) {
	resolved, itemType, resolveErr := resolveArg(dpmDir, arg)
	if resolveErr != nil {
		return "", resolveErr
	}

	switch itemType {
	case "bundle":
		return resolved, nil
	case "tool", "profile":
		return "", fmt.Errorf("[%s] is a %s, not a community profile\n       Use 'dpm install %s' or 'dpm apply %s' instead", arg, itemType, resolved, resolved)
	default:
		if !strings.HasPrefix(arg, "https://") {
			return "", fmt.Errorf("provide a GitHub URL or a number from 'dpm search -c <query>'")
		}
		return arg, nil
	}
}

func inspectDotfile(provider *search.GitHubProvider, repoURL, filename string) error {
	fmt.Printf("Fetching %s from %s ...\n\n", filename, repoURL)
	raw, fileErr := search.FetchDotfile(context.Background(), provider, repoURL, filename)
	if fileErr != nil {
		return fmt.Errorf("could not fetch '%s': %w", filename, fileErr)
	}
	fmt.Printf("─── %s ───\n\n", filename)
	fmt.Println(string(raw))
	return nil
}

func splitRepoOwner(repoURL string) (string, string) {
	owner, repo := "", repoURL
	parts := strings.Split(strings.TrimPrefix(repoURL, "https://github.com/"), "/")
	if len(parts) == 2 {
		owner, repo = parts[0], parts[1]
	}
	return owner, repo
}

func printBundleToolSummary(bundle *search.ProfileBundle, knownIDs map[string]struct{}) {
	fmt.Printf("  Tools (%d):\n", len(bundle.Tools))
	for _, id := range bundle.Tools {
		if _, ok := knownIDs[id]; ok {
			fmt.Printf("    %-20s ✓ in catalog\n", id)
		} else {
			fmt.Printf("    %-20s ✗ not in local catalog (will be skipped)\n", id)
		}
	}
}

func printBundleDotfileSummary(bundle *search.ProfileBundle, arg string) {
	if len(bundle.Dotfiles) == 0 {
		return
	}

	fmt.Printf("  Dotfiles (%d):\n", len(bundle.Dotfiles))
	for _, df := range bundle.Dotfiles {
		fmt.Printf("    %s\n", df)
	}
	fmt.Println()
	fmt.Printf("Tip: view a dotfile:  dpm inspect %s <filename>\n", arg)
}

func inspectBundle(provider *search.GitHubProvider, repoURL string, knownIDs map[string]struct{}, arg string) error {
	fmt.Printf("Fetching %s ...\n\n", repoURL)
	bundle, fetchErr := search.Fetch(context.Background(), provider, repoURL, knownIDs)
	if fetchErr != nil {
		return fmt.Errorf("fetch failed: %w", fetchErr)
	}

	owner, repo := splitRepoOwner(repoURL)
	fmt.Printf("Community profile: %s/%s\n", owner, repo)
	if bundle.Description != "" {
		fmt.Printf("  Description:  %s\n", bundle.Description)
	}
	if bundle.Version != "" {
		fmt.Printf("  Version:      %s\n", bundle.Version)
	}
	fmt.Printf("  Source:       %s\n", repoURL)
	fmt.Println()

	if len(bundle.Tools) > 0 {
		printBundleToolSummary(bundle, knownIDs)
		fmt.Println()
	} else {
		fmt.Println("  Tools:  (none)")
		fmt.Println()
	}

	printBundleDotfileSummary(bundle, arg)
	fmt.Printf("Install:              dpm apply %s\n", repoURL)
	return nil
}

func runInspect(arg, filename, dpmDir string, eng interface {
	Catalog() ([]catalog.Tool, error)
	GetDPMRoot() string
}) error {
	repoURL, err := resolveInspectRepoURL(arg, dpmDir)
	if err != nil {
		return err
	}

	tools, err := eng.Catalog()
	if err != nil {
		return fmt.Errorf("load catalog: %w", err)
	}
	knownIDs := catalog.ToolIDSet(tools)

	provider := &search.GitHubProvider{}

	if filename != "" {
		return inspectDotfile(provider, repoURL, filename)
	}

	return inspectBundle(provider, repoURL, knownIDs, arg)
}

// newEngine creates an engine with embedded catalog/profiles.
func newEngine() (*engine.Engine, error) {
	return engine.New(func(cfg *engine.Config) {
		cfg.EmbeddedCatalog = EmbeddedCatalog
		cfg.EmbeddedProfiles = EmbeddedProfiles
	})
}

// cliToolMatch is a thin wrapper for CLI tool lookup.
type cliToolMatch struct {
	tool catalog.Tool
}

// cliVersionMatch is a thin wrapper for CLI version lookup.
type cliVersionMatch struct {
	version catalog.ToolVersion
}

// profileMatch is a thin wrapper for CLI profile lookup.
type profileMatch struct {
	profile profiles.Profile
}

// findTUIBinary locates the dpm-tui binary next to this executable,
// in a sibling tui/target/release/ directory, or on PATH.
func findTUIBinary() string {
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		// Same directory as dpm (e.g. both installed to ~/.dpm/bin)
		if p := filepath.Join(dir, dpmTUIBinaryName); fileExists(p) {
			return p
		}
		// Development layout: dpm sits at project root, TUI at tui/target/release/
		if p := filepath.Join(dir, "tui", "target", "release", dpmTUIBinaryName); fileExists(p) {
			return p
		}
	}
	// Fall back to PATH
	if p, err := exec.LookPath(dpmTUIBinaryName); err == nil {
		return p
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func splitToolSpec(arg string) (toolID, version string) {
	toolID = arg
	if idx := strings.Index(arg, "@"); idx > 0 {
		toolID = arg[:idx]
		version = arg[idx+1:]
	}
	return toolID, version
}

func firstInstalledVersion(eng *engine.Engine, toolID string) (string, error) {
	records, err := eng.InstalledRecords()
	if err != nil {
		return "", fmt.Errorf("failed to load installed records: %w", err)
	}
	for _, r := range records {
		if r.ToolID == toolID {
			return r.Version, nil
		}
	}
	return "", fmt.Errorf("tool %q is not installed", toolID)
}

func removeToolSpec(eng *engine.Engine, arg string) (string, string, error) {
	toolID, version := splitToolSpec(arg)
	if version == "" {
		resolved, err := firstInstalledVersion(eng, toolID)
		if err != nil {
			return toolID, "", err
		}
		version = resolved
	}
	if err := eng.RemoveTool(toolID, version); err != nil {
		return toolID, version, err
	}
	return toolID, version, nil
}

func printSettingsGroups(groups []settings.SettingsGroup) {
	if len(groups) == 0 {
		fmt.Println("No settings available.")
		return
	}
	for _, group := range groups {
		fmt.Println(group.Name)
		for _, s := range group.Settings {
			value := s.Value
			if value == "" {
				value = "(empty)"
			}
			fmt.Printf("  %-24s %-8s %s\n", s.ID, value, s.Description)
		}
		fmt.Println()
	}
}

var toolCommands = []*cli.Command{
	{
		Name: "install", Usage: "Install a tool",
		UsageText:   "dpm install [--verbose|-v] <tool[@version]|course-code|search-number>",
		Description: "Install a tool or apply a course profile.\n\nExamples:\n  dpm install binwalk         # latest version\n  dpm install binwalk@2.3.4   # exact version\n  dpm install binwalk@2       # major version (e.g., v2.x)\n  dpm install ICI012AS3A      # course code — installs the full profile",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "verbose", Aliases: []string{"v"}, Usage: "Show detailed output"},
		},
		Action: func(c *cli.Context) error {
			if c.Args().Len() < 1 {
				return fmt.Errorf("specify a tool or course code\n\nExample: dpm install binwalk\n         dpm install ICI012AS3A")
			}
			arg := c.Args().First()

			eng, err := newEngine()
			if err != nil {
				return wrapInitEngineErr(err)
			}

			// Numeric shortcut from last 'dpm search' result.
			if resolved, itemType, resolveErr := resolveArg(eng.GetDPMRoot(), arg); resolveErr != nil {
				return resolveErr
			} else if itemType == "bundle" {
				fmt.Printf("Community profile: %s\n", resolved)
				fmt.Println("Hint: remote community profile install is not yet wired up in the CLI.")
				fmt.Printf("      You can apply it manually once support lands: dpm apply %s\n", resolved)
				return nil
			} else {
				arg = resolved
			}

			// Course code detection: if the argument contains no "@" and matches
			// a profile by CourseCode or ID, apply the full profile instead.
			if !strings.Contains(arg, "@") {
				profile, err := eng.FindProfileByCourseCode(arg)
				if err != nil {
					return fmt.Errorf("failed to look up course code: %w", err)
				}
				if profile != nil {
					fmt.Printf("Course profile detected: %s — %s\n", profile.Name, profile.Description)
					fmt.Printf("Tools: %s\n\n", strings.Join(profile.AllToolIDs(), ", "))
					result, err := eng.ApplyProfile(*profile)
					if err != nil {
						return fmt.Errorf("apply profile failed: %w", err)
					}
					installed, skipped, failed := 0, 0, 0
					for _, s := range result.Tools {
						if s.Err != nil && !s.Skipped {
							fmt.Printf("  FAIL  %s: %v\n", s.ToolID, s.Err)
							failed++
						} else if s.Skipped {
							fmt.Printf("  SKIP  %s@%s (already installed)\n", s.ToolID, s.Version)
							skipped++
						} else {
							fmt.Printf("  OK    %s@%s\n", s.ToolID, s.Version)
							installed++
						}
					}
					fmt.Printf("\nProfile %s: %d installed, %d skipped, %d failed\n",
						profile.Name, installed, skipped, failed)
					return nil
				}
			}

			// No profile match — treat as a plain tool install.
			toolID := arg
			versionStr := ""
			if idx := strings.Index(arg, "@"); idx > 0 {
				toolID = arg[:idx]
				versionStr = arg[idx+1:]
			}

			verbose := c.Bool("verbose")
			if !verbose {
				log.SetOutput(io.Discard)
			}

			tools, err := eng.Catalog()
			if err != nil {
				return fmt.Errorf("failed to load catalog: %w", err)
			}

			// Find the requested tool.
			var found *cliToolMatch
			for i := range tools {
				if tools[i].ID == toolID {
					found = &cliToolMatch{tool: tools[i]}
					break
				}
			}
			if found == nil {
				return fmt.Errorf("%q not found — not a tool in the catalog or a known course code", toolID)
			}

			// Resolve version: exact match "2.3.4", major match "2", or default.
			var ver *cliVersionMatch
			if versionStr != "" {
				// Try exact version match first.
				for j := range found.tool.Versions {
					if found.tool.Versions[j].Version == versionStr {
						ver = &cliVersionMatch{version: found.tool.Versions[j]}
						break
					}
				}
				// Try major version match (e.g., "2" → MajorVersion==2).
				if ver == nil {
					var major int
					if _, err := fmt.Sscanf(versionStr, "%d", &major); err == nil {
						if mv := found.tool.GetVersionByMajor(major); mv != nil {
							ver = &cliVersionMatch{version: *mv}
						}
					}
				}
				if ver == nil {
					return fmt.Errorf("version %q not found for tool %s", versionStr, toolID)
				}
			} else {
				dv := found.tool.GetDefaultVersion()
				if dv == nil {
					return fmt.Errorf("no default version available for %s", toolID)
				}
				ver = &cliVersionMatch{version: *dv}
			}

			if verbose {
				fmt.Printf("Installing %s@%s...\n", toolID, ver.version.Version)
				log.SetOutput(os.Stderr)
			}

			var sp *cliSpinner
			if !verbose {
				sp = newSpinner(fmt.Sprintf("Installing %s@%s...", toolID, ver.version.Version))
				sp.Start()
			}

			result, err := eng.InstallTool(found.tool, ver.version)

			if sp != nil {
				sp.Stop()
			}
			if err != nil {
				fmt.Printf("  ✗ %s@%s failed: %v\n", toolID, ver.version.Version, err)
				return fmt.Errorf("install failed: %w", err)
			}

			// Show result.
			verified := ""
			if result.InstallResult.Symlinks != nil {
				verified = " [SHA256 OK]"
			}
			_ = verified
			fmt.Printf("  ✓ %s@%s installed\n", toolID, ver.version.Version)
			return nil
		},
	},
	{
		Name: "remove", Usage: "Remove a tool",
		UsageText:   "dpm remove <tool[@version]> [tool[@version] ...]",
		Description: "Remove one or more installed tools.\n\nExamples:\n  dpm remove binwalk       # remove (auto-detect version)\n  dpm remove binwalk@2.3.4 # remove specific version\n  dpm remove nmap jq       # remove multiple tools",
		Action: func(c *cli.Context) error {
			if c.Args().Len() < 1 {
				return fmt.Errorf("specify a tool to remove")
			}

			eng, err := newEngine()
			if err != nil {
				return wrapInitEngineErr(err)
			}

			failed := 0
			for i := 0; i < c.Args().Len(); i++ {
				arg := c.Args().Get(i)
				toolID, version := splitToolSpec(arg)
				if version == "" {
					fmt.Printf("Removing %s...\n", toolID)
				} else {
					fmt.Printf("Removing %s@%s...\n", toolID, version)
				}
				removedID, removedVersion, err := removeToolSpec(eng, arg)
				if err != nil {
					fmt.Printf("  FAIL  %s: %v\n", arg, err)
					failed++
					continue
				}
				fmt.Printf("  OK    %s@%s\n", removedID, removedVersion)
			}
			if failed > 0 {
				return fmt.Errorf("remove failed for %d item(s)", failed)
			}
			return nil
		},
	},
	{
		Name: "update", Usage: "Update installed tools to their latest catalog version",
		UsageText:   "dpm update [--all|-a] [tool-id]",
		Description: "Update one tool, update all outdated tools, or check installed tools for available updates.\n\nExamples:\n  dpm update           # show update status for all installed tools\n  dpm update --all     # update every outdated installed tool\n  dpm update nmap      # update nmap to latest catalog version",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "all", Aliases: []string{"a"}, Usage: "Update every installed tool with an available catalog update"},
		},
		Action: func(c *cli.Context) error {
			eng, err := newEngine()
			if err != nil {
				return wrapInitEngineErr(err)
			}
			if c.Bool("all") && c.Args().Len() > 0 {
				return fmt.Errorf("use either 'dpm update --all' or 'dpm update <tool>', not both")
			}

			if c.Bool("all") {
				statuses, err := eng.CheckUpdates()
				if err != nil {
					return fmt.Errorf("failed to check updates: %w", err)
				}
				updated, failed := 0, 0
				for _, s := range statuses {
					if !s.UpdateRequired {
						continue
					}
					fmt.Printf("Updating %s (%s → %s)...\n", s.ToolID, s.InstalledVer, s.AvailableVer)
					if _, err := eng.UpdateTool(s.ToolID); err != nil {
						fmt.Printf("  FAIL  %s: %v\n", s.ToolID, err)
						failed++
						continue
					}
					fmt.Printf("  OK    %s@%s\n", s.ToolID, s.AvailableVer)
					updated++
				}
				if updated == 0 && failed == 0 {
					fmt.Println("All tools are up to date.")
					return nil
				}
				fmt.Printf("\nUpdate complete: %d updated, %d failed\n", updated, failed)
				if failed > 0 {
					return fmt.Errorf("update failed for %d tool(s)", failed)
				}
				return nil
			}

			if c.Args().Len() == 0 {
				// No argument — show update status for all installed tools.
				statuses, err := eng.CheckUpdates()
				if err != nil {
					return fmt.Errorf("failed to check updates: %w", err)
				}
				if len(statuses) == 0 {
					fmt.Println("No tools installed.")
					return nil
				}
				updates := 0
				for _, s := range statuses {
					if s.NotInCatalog {
						fmt.Printf("  ?  %-12s v%s (no longer in catalog)\n", s.ToolID, s.InstalledVer)
					} else if s.UpdateRequired {
						fmt.Printf("  ↑  %-12s v%s → v%s\n", s.ToolID, s.InstalledVer, s.AvailableVer)
						updates++
					} else {
						fmt.Printf("  ✓  %-12s v%s (up to date)\n", s.ToolID, s.InstalledVer)
					}
				}
				if updates > 0 {
					fmt.Printf("\n%d update(s) available. Run 'dpm update <tool>' to update.\n", updates)
				} else {
					fmt.Println("\nAll tools are up to date.")
				}
				return nil
			}

			// Argument given — update that specific tool.
			toolID := c.Args().First()
			fmt.Printf("Updating %s...\n", toolID)
			result, err := eng.UpdateTool(toolID)
			if err != nil {
				return fmt.Errorf("update failed: %w", err)
			}
			fmt.Printf("Updated %s → installed into %s\n", toolID, result.InstallDir)
			return nil
		},
	},
	{
		Name: "list", Usage: "List tools",
		UsageText:   "dpm list [--all|-a] [--category|-c <name>]",
		Description: "List installed or available tools",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "all", Aliases: []string{"a"}, Usage: "List all available tools in catalog"},
			&cli.StringFlag{Name: "category", Aliases: []string{"c"}, Usage: "Filter by category (e.g., 'security', 'development')"},
		},
		Action: func(c *cli.Context) error {
			eng, err := newEngine()
			if err != nil {
				return wrapInitEngineErr(err)
			}
			if c.Bool("all") {
				tools, err := eng.Catalog()
				if err != nil {
					return fmt.Errorf("failed to load catalog: %w", err)
				}
				if len(tools) == 0 {
					fmt.Println("Dpm is still empty - building around.")
					return nil
				}
				catFilter := c.String("category")
				fmt.Println("Available tools in catalog:")
				fmt.Println()
				for _, t := range tools {
					if catFilter != "" && t.Category != catFilter {
						continue
					}
					for _, v := range t.Versions {
						status := "  "
						if v.Installed {
							status = "✓ "
						}
						latest := ""
						if v.IsLatest {
							latest = " [latest]"
						}
						fmt.Printf("  %s%-12s v%-8s - %s%s\n", status, t.ID, v.Version, t.Description, latest)
					}
				}
				fmt.Println()
				fmt.Println("Use 'dpm install <tool>' to install")
			} else {
				records, err := eng.InstalledRecords()
				if err != nil {
					return fmt.Errorf("failed to load installed records: %w", err)
				}
				if len(records) == 0 {
					fmt.Println("No tools installed.")
					fmt.Println("Use 'dpm list --all' to see available tools.")
					return nil
				}
				fmt.Println("Installed tools:")
				fmt.Println()
				for _, r := range records {
					fmt.Printf("  ✓ %-12s v%s\n", r.ToolID, r.Version)
				}
				fmt.Printf("\nTotal: %d packages\n", len(records))
			}
			return nil
		},
	},
}

var discoveryCommands = []*cli.Command{
	{
		Name:        "search",
		Usage:       "Search for tools, profiles, and community profiles",
		UsageText:   "dpm search [keyword] [--all|-a] [--tools|-t] [--profiles|-p] [--community|-c]",
		ArgsUsage:   "[keyword]",
		Description: "Fuzzy-search the tool catalog, local profiles, and the community profile index.\nResults are numbered — use the number directly in install/apply commands.\n\nFILTER FLAGS:\n  -t, --tools      Show tools only\n  -p, --profiles   Show local profiles only\n  -c, --community  Show community profiles only\n  -a, --all        List everything without a keyword\n\nFlags can be combined and used with or without a keyword:\n  dpm search nmap              Search all sections for 'nmap'\n  dpm search nmap -t           Search tools only\n  dpm search nmap -t -c        Search tools and community profiles\n  dpm search -t                List all tools\n  dpm search -a                List everything\n\nAfter a search, use result numbers directly:\n  dpm install 1                Install result [1] (if it is a tool)\n  dpm apply 3                  Apply result [3] (if it is a profile)\n  dpm config inspect 4         Inspect result [4] (if it is a community profile)",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "all", Aliases: []string{"a"}, Usage: "List everything (no keyword required)"},
			&cli.BoolFlag{Name: "tools", Aliases: []string{"t"}, Usage: "Search tools only"},
			&cli.BoolFlag{Name: "profiles", Aliases: []string{"p"}, Usage: "Search local profiles only"},
			&cli.BoolFlag{Name: "community", Aliases: []string{"c"}, Usage: "Search community profiles only"},
		},
		Action: func(c *cli.Context) error {
			showAll := c.Bool("all")
			onlyTools := c.Bool("tools")
			onlyProfiles := c.Bool("profiles")
			onlyCommunity := c.Bool("community")

			// Determine which sections to show. No filter flags = show all.
			filterActive := onlyTools || onlyProfiles || onlyCommunity
			showTools := !filterActive || onlyTools
			showProfiles := !filterActive || onlyProfiles
			showCommunity := !filterActive || onlyCommunity

			query := c.Args().First()
			if query == "" && !showAll && !filterActive {
				return fmt.Errorf("usage: dpm search <keyword>\n       dpm search --all       (list everything)\n       dpm search -t          (list all tools)\n       dpm search -p          (list all profiles)\n       dpm search nmap -t     (search tools only)")
			}

			eng, err := newEngine()
			if err != nil {
				return wrapEngineErr(err)
			}
			dpmDir := eng.GetDPMRoot()

			var rankedTools []catalog.Tool
			if showTools {
				tools, err := eng.Catalog()
				if err != nil {
					return fmt.Errorf("load catalog: %w", err)
				}
				rankedTools = search.RankTools(tools, query)
			}

			var rankedProfiles []profiles.Profile
			if showProfiles {
				profs, err := eng.Profiles()
				if err != nil {
					return fmt.Errorf("load profiles: %w", err)
				}
				rankedProfiles = search.RankProfiles(profs, query)
			}

			var rankedBundles []search.Entry
			var idxErr error
			if showCommunity {
				var idx *search.Index
				idx, idxErr = search.LoadIndex(dpmDir)
				if idxErr == nil {
					rankedBundles = search.RankBundles(idx.Entries(), query)
				}
			}

			total := len(rankedTools) + len(rankedProfiles) + len(rankedBundles)
			if total == 0 {
				if query != "" {
					fmt.Printf("No results for '%s'.\n", query)
					fmt.Println("Tip: try a broader term, or run 'dpm search --all' to browse everything.")
				} else {
					fmt.Println("No items found.")
				}
				return nil
			}

			// Build last-search index while printing results.
			ls := lastSearch{Query: query}
			n := 1

			if query != "" {
				fmt.Printf("Search results for '%s':\n\n", query)
			} else {
				fmt.Println("All items:")
			}

			if len(rankedTools) > 0 {
				fmt.Println("  TOOLS")
				fmt.Println("  ─────")
				for _, t := range rankedTools {
					fmt.Printf("  [%d] %-20s %s\n", n, t.ID, t.Description)
					ls.Items = append(ls.Items, lastSearchItem{N: n, Type: "tool", ID: t.ID})
					n++
				}
				fmt.Println()
			}

			if len(rankedProfiles) > 0 {
				fmt.Println("  PROFILES")
				fmt.Println("  ────────")
				for _, p := range rankedProfiles {
					extra := ""
					if p.CourseCode != "" {
						extra = "  [" + p.CourseCode + "]"
					}
					fmt.Printf("  [%d] %-20s %s%s\n", n, p.ID, p.Description, extra)
					ls.Items = append(ls.Items, lastSearchItem{N: n, Type: "profile", ID: p.ID})
					n++
				}
				fmt.Println()
			}

			if len(rankedBundles) > 0 {
				fmt.Println("  COMMUNITY PROFILES")
				fmt.Println("  ──────────────────")
				for _, b := range rankedBundles {
					stars := ""
					if b.Stars > 0 {
						stars = fmt.Sprintf("  ★%d", b.Stars)
					}
					displayName := b.Owner + "/" + b.RepoName
					fmt.Printf("  [%d] %-30s %s%s\n", n, displayName, b.Description, stars)
					ls.Items = append(ls.Items, lastSearchItem{N: n, Type: "bundle", URL: b.RepoURL, Name: displayName})
					n++
				}
				fmt.Println()
			}

			if idxErr != nil {
				fmt.Printf("  (community index unavailable: %v)\n\n", idxErr)
			}

			saveLastSearch(dpmDir, ls)

			fmt.Println("Next steps:")
			fmt.Println("  dpm install <id or number>   — install a tool")
			fmt.Println("  dpm apply <id or number>     — apply a profile")
			if len(ls.Items) > 0 {
				first := ls.Items[0]
				switch first.Type {
				case "tool":
					fmt.Printf("  dpm install %d               — installs %s\n", first.N, first.ID)
				case "profile":
					fmt.Printf("  dpm apply %d                 — applies %s\n", first.N, first.ID)
				}
			}
			return nil
		},
	},
	{
		Name:        "inspect",
		Usage:       "Inspect a community profile before installing",
		UsageText:   "dpm inspect <github-url|search-number> [dotfile]",
		ArgsUsage:   "<github-url or search-number> [dotfile]",
		Description: "Fetch and display a remote community profile's tools and dotfiles without installing anything.\nPass a dotfile name as the second argument to view its raw contents.\n\nExamples:\n  dpm inspect https://github.com/user/my-profile\n  dpm inspect 4               # number from 'dpm search -c <query>'\n  dpm inspect 4 .tmux.conf    # view a specific dotfile's raw contents",
		Action: func(c *cli.Context) error {
			if c.Args().Len() < 1 {
				return fmt.Errorf("usage: dpm inspect <github-url or search-number> [dotfile]")
			}
			eng, err := newEngine()
			if err != nil {
				return wrapEngineErr(err)
			}
			return runInspect(c.Args().Get(0), c.Args().Get(1), eng.GetDPMRoot(), eng)
		},
	},
	{
		Name: "verify", Usage: "Verify a file's SHA-256 hash",
		UsageText:   "dpm verify <file> <expected-sha256-or-hash-file>",
		Description: "Compute the SHA-256 digest of a file and compare it to an expected value.\n\nExamples:\n  dpm verify /path/to/file abc123...  # compare to inline hash\n  dpm verify /path/to/file hash.txt   # compare to hash stored in a file",
		Action: func(c *cli.Context) error {
			if c.Args().Len() < 2 {
				return fmt.Errorf("usage: verify <file> <expected-sha256-or-hash-file>")
			}
			filePath := c.Args().Get(0)
			hashArg := c.Args().Get(1)

			// If hashArg looks like a path to an existing file, read the hash from it.
			expected := hashArg
			if info, err := os.Stat(hashArg); err == nil && !info.IsDir() {
				data, err := os.ReadFile(hashArg)
				if err != nil {
					return fmt.Errorf("read hash file: %w", err)
				}
				expected = strings.TrimSpace(strings.Fields(string(data))[0])
			}

			calculated, err := sha256File(filePath)
			if err != nil {
				return fmt.Errorf("hash file: %w", err)
			}

			if strings.EqualFold(calculated, expected) {
				fmt.Printf("SHA-256 verification PASSED\n")
				fmt.Printf("File:  %s\n", filePath)
				fmt.Printf("Hash:  %s\n", calculated)
			} else {
				fmt.Printf("SHA-256 verification FAILED\n")
				fmt.Printf("File:      %s\n", filePath)
				fmt.Printf("Expected:  %s\n", expected)
				fmt.Printf("Actual:    %s\n", calculated)
				os.Exit(1)
			}
			return nil
		},
	},
	{
		Name: "version", Usage: printVersionLabel,
		UsageText: "dpm version",
		Action: func(c *cli.Context) error {
			printVersion(c.App)
			return nil
		},
	},
}

var profileCommands = []*cli.Command{
	{
		Name: "apply", Usage: "Apply a tool profile",
		UsageText:   "dpm apply <profile-id|search-number>",
		Description: "Apply a curated profile that installs multiple tools and configs at once.\n\nExample: dpm apply ICI012AS3A",
		Action: func(c *cli.Context) error {
			if c.Args().Len() < 1 {
				// List available profiles.
				eng, err := newEngine()
				if err != nil {
					return wrapInitEngineErr(err)
				}
				profs, err := eng.Profiles()
				if err != nil {
					return fmt.Errorf("failed to load profiles: %w", err)
				}
				if len(profs) == 0 {
					fmt.Println("Dpm is still empty - building around.")
					return nil
				}
				fmt.Println("Available profiles:")
				fmt.Println()
				for _, p := range profs {
					fmt.Printf("  %-20s %s\n", p.ID, p.Description)
					fmt.Printf("  %20s Tools: %s\n", "", strings.Join(p.AllToolIDs(), ", "))
					fmt.Println()
				}
				fmt.Println("Usage: dpm apply <profile-id>")
				return nil
			}

			profileID := c.Args().First()

			eng, err := newEngine()
			if err != nil {
				return wrapInitEngineErr(err)
			}

			// Numeric shortcut from last 'dpm search' result.
			if resolved, itemType, resolveErr := resolveArg(eng.GetDPMRoot(), profileID); resolveErr != nil {
				return resolveErr
			} else if itemType == "tool" {
				fmt.Printf("Hint: '%s' is a tool — use 'dpm install %s' to install it.\n", resolved, resolved)
				return nil
			} else if itemType == "bundle" {
				fmt.Printf("Community profile: %s\n", resolved)
				fmt.Println("Hint: remote community profile install is not yet wired up in the CLI.")
				return nil
			} else {
				profileID = resolved
			}

			profs, err := eng.Profiles()
			if err != nil {
				return fmt.Errorf("failed to load profiles: %w", err)
			}

			var target *profileMatch
			for i := range profs {
				if profs[i].ID == profileID {
					target = &profileMatch{profile: profs[i]}
					break
				}
			}
			if target == nil {
				return fmt.Errorf("profile %q not found", profileID)
			}

			fmt.Printf("Applying profile: %s — %s\n", target.profile.Name, target.profile.Description)
			fmt.Printf("Tools: %s\n\n", strings.Join(target.profile.AllToolIDs(), ", "))

			result, err := eng.ApplyProfile(target.profile)
			if err != nil {
				return fmt.Errorf("apply failed: %w", err)
			}

			installed := 0
			skipped := 0
			failed := 0
			for _, s := range result.Tools {
				if s.Err != nil && !s.Skipped {
					fmt.Printf("  FAIL  %s: %v\n", s.ToolID, s.Err)
					failed++
				} else if s.Skipped {
					fmt.Printf("  SKIP  %s@%s (already installed)\n", s.ToolID, s.Version)
					skipped++
				} else {
					fmt.Printf("  OK    %s@%s\n", s.ToolID, s.Version)
					installed++
				}
			}

			fmt.Printf("\nProfile %s: %d installed, %d skipped, %d failed\n",
				target.profile.Name, installed, skipped, failed)
			return nil
		},
	},
	{
		Name: "config", Usage: "Manage dotfiles configurations",
		UsageText:   "dpm config <install|scan|inspect> [options]",
		Description: "Install, scan, and inspect dotfiles configurations",
		Subcommands: []*cli.Command{
			{
				Name: "install", Usage: "Install dotfiles from a git repo",
				UsageText:   "dpm config install [--id|-i <id>] [--map|-m <src:dst>] <repo-or-path>",
				Description: "Clone a git repo and apply dotfiles.\n\nExamples:\n  dpm config install /tmp/dotfiles-demo\n  dpm config install user/repo\n  dpm config install https://github.com/user/dotfiles",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "id", Aliases: []string{"i"}, Usage: "Dotfile ID (default: repo name)"},
					&cli.StringFlag{Name: "map", Aliases: []string{"m"}, Usage: "File mapping src:dst (e.g., 'aliases.sh:.bash_aliases')"},
				},
				Action: func(c *cli.Context) error {
					if c.Args().Len() < 1 {
						return fmt.Errorf("specify a repo or local path\n\nExample: dpm config install user/dotfiles\n         dpm config install /path/to/local/dotfiles")
					}
					source := c.Args().First()
					eng, err := newEngine()
					if err != nil {
						return wrapInitEngineErr(err)
					}

					// Build dotfile from args.
					df := dotfiles.Dotfile{
						ID:   c.String("id"),
						Name: source,
					}
					// Determine if source is local dir or git repo.
					if info, err := os.Stat(source); err == nil && info.IsDir() {
						df.SourceDir = source
					} else {
						df.SourceRepo = source
					}
					if df.ID == "" {
						// Derive ID from source.
						parts := strings.Split(strings.TrimRight(source, "/"), "/")
						df.ID = parts[len(parts)-1]
					}

					// Parse --map flags for explicit mappings.
					if mapStr := c.String("map"); mapStr != "" {
						parts := strings.SplitN(mapStr, ":", 2)
						if len(parts) == 2 {
							df.Mappings = []dotfiles.FileMapping{
								{Source: parts[0], Target: parts[1], MergeStrategy: "backup"},
							}
						}
					}

					// If no mappings, auto-discover all files in source.
					fmt.Printf("Installing dotfiles: %s\n", source)
					result, err := eng.InstallDotfile(df)
					if err != nil {
						return fmt.Errorf("dotfiles install failed: %w", err)
					}
					if len(result.Applied) > 0 {
						fmt.Printf("Applied %d files:\n", len(result.Applied))
						for _, f := range result.Applied {
							fmt.Printf("  %s\n", f)
						}
					} else {
						fmt.Println("Cloned repo. Use --map to specify file mappings.")
						if result.ClonedTo != "" {
							fmt.Printf("Source: %s\n", result.ClonedTo)
						}
					}
					return nil
				},
			},
			{
				Name: "scan", Usage: "Scan a dotfiles repo for known configs",
				UsageText:   "dpm config scan [--apply] [--scripts] <repo-or-path>",
				Description: "Clone or read a dotfiles repo, detect known config files, and optionally apply all detected non-script configs. Scripts are never applied unless --scripts is also set.",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "apply", Usage: "Apply all detected non-script configs after scanning"},
					&cli.BoolFlag{Name: "scripts", Usage: "Include detected install scripts when used with --apply"},
				},
				Action: func(c *cli.Context) error {
					if c.Args().Len() < 1 {
						return fmt.Errorf("usage: dpm config scan [--apply] [--scripts] <repo-or-path>")
					}
					eng, err := newEngine()
					if err != nil {
						return wrapInitEngineErr(err)
					}
					scan, err := eng.ScanGitDotfiles(c.Args().First())
					if err != nil {
						return fmt.Errorf("dotfiles scan failed: %w", err)
					}
					if len(scan.Configs) == 0 {
						fmt.Println("No known dotfile configs detected.")
						return nil
					}
					plan, planErr := eng.PlanImportedDotfiles(scan.RepoDir, scan.Configs)
					riskBySource := make(map[string]string)
					issueBySource := make(map[string]string)
					if planErr == nil {
						for _, item := range plan.Items {
							riskBySource[item.Source] = string(item.Risk)
							if len(item.Issues) > 0 {
								issueBySource[item.Source] = strings.Join(item.Issues, "; ")
							}
						}
					}

					fmt.Printf("Detected %d config(s) in %s:\n", len(scan.Configs), scan.RepoDir)
					for _, cfg := range scan.Configs {
						kind := "config"
						if cfg.IsScript {
							kind = "script"
						}
						risk := riskBySource[filepath.Join(scan.RepoDir, cfg.Source)]
						if cfg.IsScript {
							risk = "high"
						}
						if risk == "" {
							risk = "unknown"
						}
						fmt.Printf("  %-8s %-20s %-7s %s -> %s (%s)\n", kind, cfg.Name, risk, cfg.Source, cfg.Target, cfg.MergeStrategy)
						if issue := issueBySource[filepath.Join(scan.RepoDir, cfg.Source)]; issue != "" {
							fmt.Printf("  %-8s %-20s         %s\n", "", "", issue)
						}
					}
					if planErr != nil {
						fmt.Printf("\nPlan warning: %v\n", planErr)
					} else if plan.Blocked {
						fmt.Println("\nOne or more configs are blocked by dotfile safety checks and will not be applied.")
					}

					if !c.Bool("apply") {
						fmt.Println("\nUse 'dpm config scan --apply <repo>' to apply detected non-script configs.")
						return nil
					}

					selected := make([]engine.ScannedDotfileConfig, 0, len(scan.Configs))
					for _, cfg := range scan.Configs {
						if cfg.IsScript && !c.Bool("scripts") {
							continue
						}
						if cfg.IsScript {
							cfg.AllowScript = true
						}
						selected = append(selected, cfg)
					}
					if len(selected) == 0 {
						fmt.Println("Nothing selected to apply. Use --scripts to include install scripts.")
						return nil
					}

					result, err := eng.ApplyImportedDotfiles(scan.RepoDir, selected)
					if err != nil {
						return fmt.Errorf("dotfiles apply failed: %w", err)
					}
					fmt.Printf("\nApplied %d config(s):\n", len(result.Applied))
					for _, path := range result.Applied {
						fmt.Printf("  %s\n", path)
					}
					return nil
				},
			},
			{
				Name:        "inspect",
				Usage:       "Inspect a community profile before installing",
				UsageText:   "dpm config inspect <github-url|search-number> [dotfile]",
				ArgsUsage:   "<github-url or search-number> [dotfile]",
				Description: "Fetch and display a remote community profile's tools and dotfiles without installing anything.\nPass a dotfile name as the second argument to view its raw contents.\n\nExamples:\n  dpm inspect https://github.com/user/my-profile\n  dpm inspect 4               # number from 'dpm search -c <query>'\n  dpm inspect 4 .tmux.conf    # view a specific dotfile's raw contents",
				Action: func(c *cli.Context) error {
					if c.Args().Len() < 1 {
						return fmt.Errorf("usage: dpm config inspect <github-url or search-number> [dotfile]")
					}
					eng, err := newEngine()
					if err != nil {
						return wrapEngineErr(err)
					}
					return runInspect(c.Args().Get(0), c.Args().Get(1), eng.GetDPMRoot(), eng)
				},
			},
		},
		Action: func(c *cli.Context) error {
			// Default action shows help
			cli.ShowSubcommandHelp(c)
			return nil
		},
	},
}

var systemCommands = []*cli.Command{
	{
		Name: "settings", Usage: "View and change DPM settings",
		UsageText:   "dpm settings <list|set|toggle|reset>",
		Description: "Manage the same persisted settings shown in the TUI settings overlay.",
		Subcommands: []*cli.Command{
			{
				Name:      "list",
				Usage:     "List current settings",
				UsageText: "dpm settings list",
				Action: func(c *cli.Context) error {
					eng, err := newEngine()
					if err != nil {
						return wrapInitEngineErr(err)
					}
					mgr := eng.GetSettings()
					if mgr == nil {
						return fmt.Errorf("settings manager is not available")
					}
					printSettingsGroups(mgr.Groups())
					return nil
				},
			},
			{
				Name:      "set",
				Usage:     "Set a setting value",
				UsageText: "dpm settings set <id> <value>",
				Action: func(c *cli.Context) error {
					if c.Args().Len() < 2 {
						return fmt.Errorf("usage: dpm settings set <id> <value>")
					}
					eng, err := newEngine()
					if err != nil {
						return wrapInitEngineErr(err)
					}
					mgr := eng.GetSettings()
					if mgr == nil {
						return fmt.Errorf("settings manager is not available")
					}
					id := c.Args().Get(0)
					value := c.Args().Get(1)
					if err := mgr.Set(id, value); err != nil {
						return err
					}
					fmt.Printf("Set %s=%s\n", id, value)
					return nil
				},
			},
			{
				Name:      "toggle",
				Usage:     "Toggle a bool setting",
				UsageText: "dpm settings toggle <id>",
				Action: func(c *cli.Context) error {
					if c.Args().Len() < 1 {
						return fmt.Errorf("usage: dpm settings toggle <id>")
					}
					eng, err := newEngine()
					if err != nil {
						return wrapInitEngineErr(err)
					}
					mgr := eng.GetSettings()
					if mgr == nil {
						return fmt.Errorf("settings manager is not available")
					}
					id := c.Args().First()
					if err := mgr.Toggle(id); err != nil {
						return err
					}
					if s := mgr.Get(id); s != nil {
						fmt.Printf("Set %s=%s\n", id, s.Value)
					}
					return nil
				},
			},
			{
				Name:      "reset",
				Usage:     "Reset a setting to its default value",
				UsageText: "dpm settings reset <id>",
				Action: func(c *cli.Context) error {
					if c.Args().Len() < 1 {
						return fmt.Errorf("usage: dpm settings reset <id>")
					}
					eng, err := newEngine()
					if err != nil {
						return wrapInitEngineErr(err)
					}
					mgr := eng.GetSettings()
					if mgr == nil {
						return fmt.Errorf("settings manager is not available")
					}
					id := c.Args().First()
					if err := mgr.Reset(id); err != nil {
						return err
					}
					if s := mgr.Get(id); s != nil {
						fmt.Printf("Reset %s=%s\n", id, s.Value)
					}
					return nil
				},
			},
		},
		Action: func(c *cli.Context) error {
			eng, err := newEngine()
			if err != nil {
				return wrapInitEngineErr(err)
			}
			mgr := eng.GetSettings()
			if mgr == nil {
				return fmt.Errorf("settings manager is not available")
			}
			printSettingsGroups(mgr.Groups())
			return nil
		},
	},
	{
		Name: "restore", Usage: "Remove all DPM-managed tools and reset to a clean state",
		UsageText:   "dpm restore [--yes|-y]",
		Description: "Uninstalls every tool DPM has installed and removes ~/.dpm/tools/ and ~/.dpm/bin/.\nThe download cache (~/.dpm/cache/) is kept so re-installation is fast.\nDotfile backups created by DPM are not touched.\n\nUse this when you want a completely clean slate after a course.",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "yes", Aliases: []string{"y"}, Usage: "Skip confirmation prompt"},
		},
		Action: func(c *cli.Context) error {
			eng, err := newEngine()
			if err != nil {
				return wrapInitEngineErr(err)
			}

			if !c.Bool("yes") {
				fmt.Println("This will remove all DPM-managed tools from your system.")
				fmt.Println("The download cache will be kept. Dotfile backups will not be touched.")
				fmt.Print("Continue? [y/N] ")

				var answer string
				fmt.Scanln(&answer)
				if answer != "y" && answer != "Y" {
					fmt.Println("Aborted.")
					return nil
				}
			}

			fmt.Println("Restoring system to clean state...")
			result, err := eng.Restore()
			if err != nil {
				return fmt.Errorf("restore failed: %w", err)
			}

			if len(result.RemovedTools) > 0 {
				fmt.Printf("Removed %d tool(s):\n", len(result.RemovedTools))
				for _, t := range result.RemovedTools {
					fmt.Printf("  ✓ %s\n", t)
				}
			} else {
				fmt.Println("No tools were installed.")
			}

			if len(result.Errors) > 0 {
				fmt.Printf("\n%d warning(s):\n", len(result.Errors))
				for _, e := range result.Errors {
					fmt.Printf("  ⚠ %v\n", e)
				}
			}

			fmt.Println("\nDone. DPM has been reset to a clean state.")
			return nil
		},
	},
	{
		Name: "serve", Usage: "Run the JSON-RPC backend over stdio (used by dpm-tui)",
		UsageText: "dpm serve --stdio",
		Description: "Expose the DPM engine as a JSON-RPC 2.0 NDJSON server.\n" +
			"Reads requests from stdin, writes responses to stdout, and engine logs to stderr.\n" +
			"Used internally by the Rust + Ratatui frontend (dpm-tui).",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "stdio", Aliases: []string{"s"}, Usage: "Use stdin/stdout for transport (currently the only option)"},
		},
		Action: func(c *cli.Context) error {
			// stdio is the only transport for now; the flag exists so we
			// can add other transports later without changing the CLI.
			if !c.Bool("stdio") {
				return fmt.Errorf("dpm serve currently requires --stdio")
			}

			// Engine logs go to stderr — stdout is reserved for JSON-RPC.
			log.SetOutput(os.Stderr)

			eng, err := newEngine()
			if err != nil {
				return wrapInitEngineErr(err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			return serve.Run(ctx, eng, os.Stdin, os.Stdout)
		},
	},
	{
		Name: "bubble", Usage: "Start an ephemeral bubble session",
		UsageText:   "dpm bubble",
		Description: "Creates a temporary DPM environment under /tmp/dpm-bubble-<id>/.\nAll tools and configs are installed there and removed when you exit.\nUse on shared machines, friend's laptop, or for risk-free testing.",
		Action: func(c *cli.Context) error {
			eng, err := newEngine()
			if err != nil {
				return wrapInitEngineErr(err)
			}
			session, err := eng.BubbleStart()
			if err != nil {
				return fmt.Errorf("failed to start bubble: %w", err)
			}

			fmt.Printf("Bubble session started: %s\n", session.RootPath)
			fmt.Printf("DPM_HOME=%s\n", session.RootPath)
			fmt.Println("All tools will be installed here. Type 'exit' to destroy the bubble.")
			fmt.Println()

			shell := session.Shell
			if shell == "" {
				shell = "/bin/sh"
			}

			cmd := exec.Command(shell)
			cmd.Dir = session.RootPath
			cmd.Env = os.Environ()
			for k, v := range session.Env {
				cmd.Env = append(cmd.Env, k+"="+v)
			}
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr

			cmd.Run() // shell exit code is intentionally ignored

			// Cleanup.
			fmt.Printf("\nDestroying bubble: %s\n", session.RootPath)
			if err := eng.BubbleStop(session.RootPath); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to clean up bubble: %v\n", err)
			} else {
				fmt.Println("Bubble destroyed. Back to normal.")
			}
			return nil
		},
	},
	{
		Name: "doctor", Usage: "Check system health and configuration",
		UsageText:   "dpm doctor [--fix]",
		Description: "Inspect the real system state: PATH, metadata integrity, and cache usage.",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "fix", Usage: "Apply safe automatic fixes, currently PATH setup"},
		},
		Action: func(c *cli.Context) error {
			eng, err := newEngine()
			if err != nil {
				return wrapInitEngineErr(err)
			}
			if c.Bool("fix") && !eng.IsInPATH() {
				if err := eng.AddToPATH(); err != nil {
					return fmt.Errorf("failed to add ~/.dpm/bin to PATH: %w", err)
				}
				fmt.Println("Added ~/.dpm/bin to your shell PATH config. Restart your shell to use it.")
				fmt.Println()
			}

			report, err := eng.Doctor()
			if err != nil {
				return fmt.Errorf("doctor failed: %w", err)
			}

			fmt.Println("DPM System Health Check")
			fmt.Println("========================")
			fmt.Println()

			warnings := 0
			fmt.Printf("  ✓  Platform: %s\n", report.Platform)
			for _, check := range report.Checks {
				if check.OK {
					fmt.Printf("  ✓  %s: %s\n", check.Name, check.Message)
				} else {
					fmt.Printf("  ⚠  %s: %s\n", check.Name, check.Message)
					warnings++
				}
			}

			fmt.Println()
			if warnings == 0 {
				fmt.Println("System is ready.")
			} else {
				fmt.Printf("%d warning(s) found.\n", warnings)
			}
			return nil
		},
	},
}

var appCommands = buildAppCommands()

func buildAppCommands() []*cli.Command {
	commands := make([]*cli.Command, 0, len(toolCommands)+len(discoveryCommands)+len(profileCommands)+len(systemCommands))
	commands = append(commands, toolCommands...)
	commands = append(commands, discoveryCommands...)
	commands = append(commands, profileCommands...)
	commands = append(commands, systemCommands...)
	return commands
}

func launchTUI() error {
	tuiBin := findTUIBinary()
	if tuiBin == "" {
		return fmt.Errorf("%s not found — build it with: cd tui && cargo build --release", dpmTUIBinaryName)
	}

	self, _ := os.Executable()
	cmd := exec.Command(tuiBin)
	cmd.Env = append(os.Environ(), "DPM_BIN="+self, "DPM_VERSION="+version)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return fmt.Errorf("%s: %w", dpmTUIBinaryName, err)
	}

	return nil
}

func runAppAction(c *cli.Context) error {
	if c.Bool("version") {
		printVersion(c.App)
		return nil
	}
	if c.Args().Len() > 0 {
		cli.ShowAppHelp(c)
		return nil
	}

	// Launch the Rust TUI (dpm-tui). It talks to us via `dpm serve --stdio`.
	return launchTUI()
}

func newCLIApp() *cli.App {
	app := &cli.App{
		Name:                   "dpm",
		Usage:                  asciiLogo() + "\n\nDumb Package Manager - Install tools and configs easily",
		Version:                version,
		HideVersion:            true,
		UseShortOptionHandling: true,
		Authors:                []*cli.Author{{Name: "Ilja Ylikangas, Robin Niinemets, Henry Isakoff"}},
		Action:                 runAppAction,

		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "version", Aliases: []string{"v"}, Usage: printVersionLabel},
		},

		Commands: appCommands,

		CommandNotFound: func(c *cli.Context, command string) {
			fmt.Fprintf(c.App.Writer, "Error: Unknown command '%s'\n\n", command)
			printVersion(c.App)
		},
	}

	return app
}

func main() {
	configureHelpPrinter()

	app := newCLIApp()

	enableShortOptionHandling(app.Commands)

	args := rewriteAliasArgs(os.Args)
	if err := app.Run(args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

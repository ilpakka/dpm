package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"dpm.fi/dpm/internal/catalog"
	"dpm.fi/dpm/internal/dotfiles"
	"dpm.fi/dpm/internal/engine"
	"dpm.fi/dpm/internal/metadata"
	"dpm.fi/dpm/internal/profiles"
	"dpm.fi/dpm/internal/settings"
)

// fakeEngine is a stand-in for *engine.Engine in serve tests. It records the
// methods that were invoked so tests can assert on dispatch behaviour.
type fakeEngine struct {
	platform        catalog.Platform
	dpmRoot         string
	inPath          bool
	tools           []catalog.Tool
	profiles        []profiles.Profile
	dotfiles        []dotfiles.Dotfile
	installed       []metadata.InstalledTool
	updates         []engine.UpdateStatus
	installResp     *engine.InstallResult
	profileResp     *engine.ProfileResult
	restoreResp     *engine.RestoreResult
	dotfileResp     *dotfiles.ApplyResult
	doctorResp      *engine.DoctorReport
	bubbleResp      *engine.BubbleSession
	scanResp        *engine.DotfileScanResult
	binaryPathResp  string
	settingsMgr     *settings.Manager
	logger          engine.Logger
	addPathErr      error
	installErr      error
	removeCalled    bool
	bubbleStopArg   string
	scanRepoURLArg  string
	importedRepoDir string
	logLines        []string
}

func (f *fakeEngine) Platform() catalog.Platform { return f.platform }
func (f *fakeEngine) GetDPMRoot() string         { return f.dpmRoot }
func (f *fakeEngine) IsInPATH() bool             { return f.inPath }
func (f *fakeEngine) AddToPATH() error           { return f.addPathErr }
func (f *fakeEngine) Catalog() ([]catalog.Tool, error) {
	return f.tools, nil
}
func (f *fakeEngine) Profiles() ([]profiles.Profile, error) {
	return f.profiles, nil
}
func (f *fakeEngine) LoadDotfiles() ([]dotfiles.Dotfile, error) {
	return f.dotfiles, nil
}
func (f *fakeEngine) InstalledRecords() ([]metadata.InstalledTool, error) {
	return f.installed, nil
}
func (f *fakeEngine) CheckUpdates() ([]engine.UpdateStatus, error) {
	return f.updates, nil
}
func (f *fakeEngine) InstallTool(tool catalog.Tool, version catalog.ToolVersion) (*engine.InstallResult, error) {
	if f.installErr != nil {
		return nil, f.installErr
	}
	// Emit a synthetic log line so streaming tests can observe it.
	if f.logger != nil {
		f.logger.Printf("installing %s@%s", tool.ID, version.Version)
	}
	return f.installResp, nil
}
func (f *fakeEngine) RemoveTool(toolID, version string) error {
	f.removeCalled = true
	return nil
}
func (f *fakeEngine) UpdateTool(toolID string) (*engine.InstallResult, error) {
	return f.installResp, nil
}
func (f *fakeEngine) ApplyProfile(profile profiles.Profile) (*engine.ProfileResult, error) {
	if f.logger != nil {
		f.logger.Printf("applying profile %s", profile.ID)
	}
	return f.profileResp, nil
}
func (f *fakeEngine) InstallDotfile(df dotfiles.Dotfile) (*dotfiles.ApplyResult, error) {
	return f.dotfileResp, nil
}
func (f *fakeEngine) Restore() (*engine.RestoreResult, error) {
	return f.restoreResp, nil
}
func (f *fakeEngine) GetSettings() *settings.Manager { return f.settingsMgr }
func (f *fakeEngine) SetLogger(l engine.Logger)          { f.logger = l }
func (f *fakeEngine) SetConfirmFunc(_ func(string) bool) {}
func (f *fakeEngine) FindProfileByCourseCode(code string) (*profiles.Profile, error) {
	for i := range f.profiles {
		if f.profiles[i].CourseCode == code || f.profiles[i].ID == code {
			return &f.profiles[i], nil
		}
	}
	return nil, nil
}
func (f *fakeEngine) Doctor() (*engine.DoctorReport, error) { return f.doctorResp, nil }
func (f *fakeEngine) BinaryPath(toolID string) (string, error) {
	return f.binaryPathResp, nil
}
func (f *fakeEngine) BubbleStart() (*engine.BubbleSession, error) {
	return f.bubbleResp, nil
}
func (f *fakeEngine) BubbleStop(rootPath string) error {
	f.bubbleStopArg = rootPath
	return nil
}
func (f *fakeEngine) ScanGitDotfiles(repoURL string) (*engine.DotfileScanResult, error) {
	f.scanRepoURLArg = repoURL
	return f.scanResp, nil
}
func (f *fakeEngine) ApplyImportedDotfiles(repoDir string, _ []engine.ScannedDotfileConfig) (*dotfiles.ApplyResult, error) {
	f.importedRepoDir = repoDir
	return f.dotfileResp, nil
}

// runOnce feeds a single request to a fresh server and returns every JSON
// message it produced (responses + notifications) in order.
func runOnce(t *testing.T, eng Engine, request string) []map[string]any {
	t.Helper()
	in := bytes.NewBufferString(request + "\n")
	var out bytes.Buffer
	if err := Run(context.Background(), eng, in, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return decodeAll(t, out.Bytes())
}

func decodeAll(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	var msgs []map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	for dec.More() {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("decode response: %v\nraw: %s", err, raw)
		}
		msgs = append(msgs, m)
	}
	return msgs
}

func findResponse(msgs []map[string]any, id float64) map[string]any {
	for _, m := range msgs {
		if v, ok := m["id"]; ok {
			if n, ok := v.(float64); ok && n == id {
				return m
			}
		}
	}
	return nil
}

func TestEnginePlatform(t *testing.T) {
	eng := &fakeEngine{platform: catalog.PlatformLinuxAMD64}
	msgs := runOnce(t, eng, `{"jsonrpc":"2.0","id":1,"method":"engine.platform"}`)
	resp := findResponse(msgs, 1)
	if resp == nil {
		t.Fatalf("no response with id=1: %v", msgs)
	}
	if got := resp["result"]; got != "linux-amd64" {
		t.Fatalf("expected linux-amd64, got %v", got)
	}
}

func TestEngineCatalog(t *testing.T) {
	tool := catalog.Tool{
		ID:          "nmap",
		Name:        "nmap",
		Description: "network mapper",
		Versions: []catalog.ToolVersion{
			{Version: "7.95", MajorVersion: 7, IsLatest: true},
		},
	}
	eng := &fakeEngine{tools: []catalog.Tool{tool}}
	msgs := runOnce(t, eng, `{"jsonrpc":"2.0","id":2,"method":"engine.catalog"}`)
	resp := findResponse(msgs, 2)
	if resp == nil {
		t.Fatalf("no response with id=2: %v", msgs)
	}
	result, ok := resp["result"].([]any)
	if !ok {
		t.Fatalf("result is not array: %v", resp["result"])
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result))
	}
	tm := result[0].(map[string]any)
	if tm["ID"] != "nmap" && tm["id"] != "nmap" {
		t.Fatalf("expected nmap, got %v", tm)
	}
}

func TestUnknownMethod(t *testing.T) {
	eng := &fakeEngine{}
	msgs := runOnce(t, eng, `{"jsonrpc":"2.0","id":3,"method":"engine.bogus"}`)
	resp := findResponse(msgs, 3)
	if resp == nil {
		t.Fatalf("no response: %v", msgs)
	}
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error object, got %v", resp)
	}
	if code := errObj["code"].(float64); code != errMethodNotFound {
		t.Fatalf("expected method-not-found code, got %v", code)
	}
}

func TestParseError(t *testing.T) {
	eng := &fakeEngine{}
	msgs := runOnce(t, eng, `not json`)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	errObj, ok := msgs[0]["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error, got %v", msgs[0])
	}
	if code := errObj["code"].(float64); code != errParse {
		t.Fatalf("expected parse error code, got %v", code)
	}
}

func TestInstallToolByID(t *testing.T) {
	tool := catalog.Tool{
		ID:   "ffuf",
		Name: "ffuf",
		Versions: []catalog.ToolVersion{
			{Version: "2.1.0", MajorVersion: 2, IsLatest: true},
		},
	}
	eng := &fakeEngine{
		tools: []catalog.Tool{tool},
		installResp: &engine.InstallResult{},
	}
	req := `{"jsonrpc":"2.0","id":4,"method":"engine.installTool","params":{"tool_id":"ffuf"}}`
	msgs := runOnce(t, eng, req)

	// Expect at least one log notification + one response.
	var sawLog bool
	for _, m := range msgs {
		if m["method"] == "log" {
			sawLog = true
		}
	}
	if !sawLog {
		t.Fatalf("expected log notification, got: %v", msgs)
	}

	resp := findResponse(msgs, 4)
	if resp == nil {
		t.Fatalf("no response with id=4: %v", msgs)
	}
	if _, hasErr := resp["error"]; hasErr {
		t.Fatalf("unexpected error: %v", resp["error"])
	}
}

func TestInstallToolError(t *testing.T) {
	eng := &fakeEngine{
		tools: []catalog.Tool{{
			ID: "x",
			Versions: []catalog.ToolVersion{{Version: "1.0", IsLatest: true}},
		}},
		installErr: errors.New("boom"),
	}
	req := `{"jsonrpc":"2.0","id":5,"method":"engine.installTool","params":{"tool_id":"x"}}`
	msgs := runOnce(t, eng, req)
	resp := findResponse(msgs, 5)
	if resp == nil {
		t.Fatalf("no response: %v", msgs)
	}
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error: %v", resp)
	}
	if !strings.Contains(errObj["message"].(string), "boom") {
		t.Fatalf("expected boom in error: %v", errObj)
	}
}

func TestRemoveTool(t *testing.T) {
	eng := &fakeEngine{}
	req := `{"jsonrpc":"2.0","id":6,"method":"engine.removeTool","params":{"tool_id":"nmap","version":"7.95"}}`
	msgs := runOnce(t, eng, req)
	if !eng.removeCalled {
		t.Fatalf("RemoveTool was not called")
	}
	resp := findResponse(msgs, 6)
	if resp == nil {
		t.Fatalf("no response: %v", msgs)
	}
	if _, hasErr := resp["error"]; hasErr {
		t.Fatalf("unexpected error: %v", resp["error"])
	}
}

func TestApplyProfileByID(t *testing.T) {
	prof := profiles.Profile{ID: "ICI012AS3A", Name: "Pen testing"}
	eng := &fakeEngine{
		profiles:    []profiles.Profile{prof},
		profileResp: &engine.ProfileResult{ProfileID: "ICI012AS3A"},
	}
	req := `{"jsonrpc":"2.0","id":7,"method":"engine.applyProfile","params":{"profile_id":"ICI012AS3A"}}`
	msgs := runOnce(t, eng, req)
	resp := findResponse(msgs, 7)
	if resp == nil {
		t.Fatalf("no response: %v", msgs)
	}
	if _, hasErr := resp["error"]; hasErr {
		t.Fatalf("unexpected error: %v", resp["error"])
	}
}

func TestNotificationNoResponse(t *testing.T) {
	// JSON-RPC notifications (no `id`) should not produce a response.
	eng := &fakeEngine{platform: catalog.PlatformLinuxAMD64}
	req := `{"jsonrpc":"2.0","method":"engine.platform"}`
	msgs := runOnce(t, eng, req)
	for _, m := range msgs {
		if _, ok := m["id"]; ok {
			t.Fatalf("did not expect a response for a notification: %v", m)
		}
	}
}

func TestEngineDoctor(t *testing.T) {
	report := &engine.DoctorReport{
		Platform: "linux-amd64",
		InPATH:   true,
		DPMRoot:  "/home/x/.dpm",
		Checks: []engine.DoctorCheck{
			{Name: "PATH", OK: true, Severity: "info", Message: "ok"},
		},
	}
	eng := &fakeEngine{doctorResp: report}
	msgs := runOnce(t, eng, `{"jsonrpc":"2.0","id":20,"method":"engine.doctor"}`)
	resp := findResponse(msgs, 20)
	if resp == nil {
		t.Fatalf("no response: %v", msgs)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("result is not object: %v", resp["result"])
	}
	if result["platform"] != "linux-amd64" {
		t.Fatalf("expected linux-amd64, got %v", result["platform"])
	}
	if result["in_path"] != true {
		t.Fatalf("expected in_path=true, got %v", result["in_path"])
	}
	checks, ok := result["checks"].([]any)
	if !ok || len(checks) == 0 {
		t.Fatalf("expected non-empty checks, got %v", result["checks"])
	}
}

func TestEngineBinaryPath(t *testing.T) {
	eng := &fakeEngine{binaryPathResp: "/home/x/.dpm/tools/nmap/7.95/nmap"}
	req := `{"jsonrpc":"2.0","id":21,"method":"engine.binaryPath","params":{"tool_id":"nmap"}}`
	msgs := runOnce(t, eng, req)
	resp := findResponse(msgs, 21)
	if resp == nil {
		t.Fatalf("no response: %v", msgs)
	}
	if got := resp["result"]; got != "/home/x/.dpm/tools/nmap/7.95/nmap" {
		t.Fatalf("unexpected result: %v", got)
	}
}

func TestEngineBinaryPathMissingID(t *testing.T) {
	eng := &fakeEngine{}
	req := `{"jsonrpc":"2.0","id":22,"method":"engine.binaryPath","params":{}}`
	msgs := runOnce(t, eng, req)
	resp := findResponse(msgs, 22)
	if _, ok := resp["error"]; !ok {
		t.Fatalf("expected error: %v", resp)
	}
}

func TestEngineBubbleStartStop(t *testing.T) {
	session := &engine.BubbleSession{
		RootPath: "/tmp/dpm-bubble-1234",
		Shell:    "/bin/zsh",
		Env:      map[string]string{"DPM_HOME": "/tmp/dpm-bubble-1234"},
	}
	eng := &fakeEngine{bubbleResp: session}
	startMsgs := runOnce(t, eng, `{"jsonrpc":"2.0","id":23,"method":"engine.bubble.start"}`)
	startResp := findResponse(startMsgs, 23)
	if startResp == nil {
		t.Fatalf("no start response: %v", startMsgs)
	}
	startResult, ok := startResp["result"].(map[string]any)
	if !ok {
		t.Fatalf("start result not object: %v", startResp["result"])
	}
	if startResult["root_path"] != "/tmp/dpm-bubble-1234" {
		t.Fatalf("unexpected root_path: %v", startResult["root_path"])
	}

	stopReq := `{"jsonrpc":"2.0","id":24,"method":"engine.bubble.stop","params":{"root_path":"/tmp/dpm-bubble-1234"}}`
	stopMsgs := runOnce(t, eng, stopReq)
	stopResp := findResponse(stopMsgs, 24)
	if stopResp == nil {
		t.Fatalf("no stop response: %v", stopMsgs)
	}
	if _, hasErr := stopResp["error"]; hasErr {
		t.Fatalf("unexpected error: %v", stopResp["error"])
	}
	if eng.bubbleStopArg != "/tmp/dpm-bubble-1234" {
		t.Fatalf("BubbleStop not called with expected path, got %q", eng.bubbleStopArg)
	}
}

func TestEngineDotfilesScan(t *testing.T) {
	eng := &fakeEngine{
		scanResp: &engine.DotfileScanResult{
			RepoDir: "/tmp/dotfiles-x",
			Configs: []engine.ScannedDotfileConfig{
				{Name: "tmux config", Source: "tmux.conf", Target: ".tmux.conf", MergeStrategy: "backup"},
			},
		},
	}
	req := `{"jsonrpc":"2.0","id":25,"method":"engine.dotfiles.scan","params":{"repo_url":"https://github.com/example/dotfiles"}}`
	msgs := runOnce(t, eng, req)
	resp := findResponse(msgs, 25)
	if resp == nil {
		t.Fatalf("no response: %v", msgs)
	}
	if eng.scanRepoURLArg != "https://github.com/example/dotfiles" {
		t.Fatalf("scan called with %q", eng.scanRepoURLArg)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("result not object: %v", resp["result"])
	}
	if result["repo_dir"] != "/tmp/dotfiles-x" {
		t.Fatalf("unexpected repo_dir: %v", result["repo_dir"])
	}
}

func TestSettingsGroups(t *testing.T) {
	dir := t.TempDir()
	mgr, err := settings.NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	eng := &fakeEngine{settingsMgr: mgr}
	msgs := runOnce(t, eng, `{"jsonrpc":"2.0","id":8,"method":"engine.settings.groups"}`)
	resp := findResponse(msgs, 8)
	if resp == nil {
		t.Fatalf("no response: %v", msgs)
	}
	result, ok := resp["result"].([]any)
	if !ok || len(result) == 0 {
		t.Fatalf("expected non-empty groups, got %v", resp["result"])
	}
}

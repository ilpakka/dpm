// Package serve exposes the DPM engine over a JSON-RPC 2.0 channel using
// newline-delimited JSON (NDJSON). It is the backend transport for the
// Rust + Ratatui frontend (`dpm-tui`).
//
// The protocol is intentionally minimal:
//
//   - Client sends `{"jsonrpc":"2.0","id":N,"method":"...","params":{}}` per line.
//   - Server replies with `{"jsonrpc":"2.0","id":N,"result":...}` or
//     `{"jsonrpc":"2.0","id":N,"error":{"code":...,"message":"..."}}` per line.
//   - Server may emit `id`-less notifications at any time, e.g. log lines:
//     `{"jsonrpc":"2.0","method":"log","params":{"line":"..."}}`.
//
// All notifications and responses are written under a mutex so concurrent
// goroutines (engine logger + request handler) cannot interleave bytes.
package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"

	"dpm.fi/dpm/internal/catalog"
	"dpm.fi/dpm/internal/dotfiles"
	"dpm.fi/dpm/internal/engine"
	"dpm.fi/dpm/internal/metadata"
	"dpm.fi/dpm/internal/profiles"
	"dpm.fi/dpm/internal/settings"
)

// Compile-time assertion: the real *engine.Engine must satisfy our interface.
var _ Engine = (*engine.Engine)(nil)

// jsonrpcVersion is the only protocol version supported.
const jsonrpcVersion = "2.0"

// Standard JSON-RPC 2.0 error codes.
const (
	errParse          = -32700
	errInvalidRequest = -32600
	errMethodNotFound = -32601
	errInvalidParams  = -32602
	errInternal       = -32603
)

// Engine is the subset of *engine.Engine that the serve layer needs.
// Defining it as an interface keeps the package testable without spinning
// up a full real engine for round-trip tests. *engine.Engine satisfies this
// interface naturally.
type Engine interface {
	Platform() catalog.Platform
	GetDPMRoot() string
	IsInPATH() bool
	AddToPATH() error
	Catalog() ([]catalog.Tool, error)
	Profiles() ([]profiles.Profile, error)
	LoadDotfiles() ([]dotfiles.Dotfile, error)
	InstalledRecords() ([]metadata.InstalledTool, error)
	CheckUpdates() ([]engine.UpdateStatus, error)
	InstallTool(tool catalog.Tool, version catalog.ToolVersion) (*engine.InstallResult, error)
	RemoveTool(toolID, version string) error
	UpdateTool(toolID string) (*engine.InstallResult, error)
	ApplyProfile(profile profiles.Profile) (*engine.ProfileResult, error)
	InstallDotfile(df dotfiles.Dotfile) (*dotfiles.ApplyResult, error)
	Restore() (*engine.RestoreResult, error)
	GetSettings() *settings.Manager
	SetLogger(l engine.Logger)
	FindProfileByCourseCode(code string) (*profiles.Profile, error)
	Doctor() (*engine.DoctorReport, error)
	BinaryPath(toolID string) (string, error)
	BubbleStart() (*engine.BubbleSession, error)
	BubbleStop(rootPath string) error
	ScanGitDotfiles(repoURL string) (*engine.DotfileScanResult, error)
	PlanImportedDotfiles(repoDir string, configs []engine.ScannedDotfileConfig) (*dotfiles.PlanResult, error)
	ApplyImportedDotfiles(repoDir string, configs []engine.ScannedDotfileConfig) (*dotfiles.ApplyResult, error)
}

// Run reads NDJSON requests from `in`, dispatches them to `eng`, and writes
// responses + notifications to `out`. It blocks until `in` returns EOF or
// `ctx` is cancelled. Returns nil on a clean EOF, otherwise the underlying
// I/O error.
//
// During the call, the engine's logger is replaced by a notification sink so
// log lines stream to the client as `log` notifications.
func Run(ctx context.Context, eng Engine, in io.Reader, out io.Writer) error {
	srv := newServer(eng, out)
	srv.installLogger()

	// Watch ctx in a goroutine so a cancellation aborts the read loop. We
	// cannot easily interrupt bufio.Scanner.Scan, but reading from a closed
	// pipe (the frontend's end) returns EOF, which is the only realistic
	// shutdown path for this transport.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
		case <-done:
		}
	}()

	scanner := bufio.NewScanner(in)
	// Allow lines up to 4 MiB so big payloads (full catalog) fit comfortably.
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		srv.handleLine(line)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("serve: read: %w", err)
	}
	return nil
}

// server holds the writer + sync state for a single Run() invocation.
type server struct {
	eng     Engine
	enc     *json.Encoder
	writeMu sync.Mutex
}

func newServer(eng Engine, out io.Writer) *server {
	return &server{
		eng: eng,
		enc: json.NewEncoder(out),
	}
}

// installLogger redirects engine log output to a notification stream.
func (s *server) installLogger() {
	s.eng.SetLogger(&notifyLogger{srv: s})
}

// notifyLogger turns engine.Printf calls into JSON-RPC `log` notifications.
type notifyLogger struct {
	srv *server
}

func (n *notifyLogger) Printf(format string, v ...any) {
	line := fmt.Sprintf(format, v...)
	n.srv.notify("log", map[string]any{"line": line})
}

// notify sends a server-initiated message (no id field).
func (s *server) notify(method string, params any) {
	msg := struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{
		JSONRPC: jsonrpcVersion,
		Method:  method,
		Params:  params,
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.enc.Encode(&msg); err != nil {
		fmt.Fprintf(os.Stderr, "serve: notify encode error: %v\n", err)
	}
}

// rpcRequest mirrors the JSON-RPC 2.0 wire format.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result"`
	Error   *rpcError       `json:"error,omitempty"`
}

// handleLine parses one NDJSON line and routes it to a method handler.
func (s *server) handleLine(line []byte) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		s.writeError(nil, errParse, "parse error: "+err.Error())
		return
	}
	if req.JSONRPC != jsonrpcVersion {
		s.writeError(req.ID, errInvalidRequest, "expected jsonrpc=2.0")
		return
	}
	if req.Method == "" {
		s.writeError(req.ID, errInvalidRequest, "missing method")
		return
	}

	result, rpcErr := s.dispatch(req.Method, req.Params)
	if rpcErr != nil {
		s.writeError(req.ID, rpcErr.Code, rpcErr.Message)
		return
	}
	// Notifications (no id) get no response.
	if len(req.ID) == 0 {
		return
	}
	s.writeResult(req.ID, result)
}

func (s *server) writeResult(id json.RawMessage, result any) {
	resp := rpcResponse{
		JSONRPC: jsonrpcVersion,
		ID:      id,
		Result:  result,
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.enc.Encode(&resp); err != nil {
		fmt.Fprintf(os.Stderr, "serve: writeResult encode error: %v\n", err)
	}
}

func (s *server) writeError(id json.RawMessage, code int, message string) {
	resp := rpcResponse{
		JSONRPC: jsonrpcVersion,
		ID:      id,
		Error:   &rpcError{Code: code, Message: message},
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.enc.Encode(&resp); err != nil {
		fmt.Fprintf(os.Stderr, "serve: writeError encode error: %v\n", err)
	}
}

// dispatch routes a method to its handler. Returns (result, nil) on success
// or (nil, rpcError) on any failure.
func (s *server) dispatch(method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "engine.platform":
		return string(s.eng.Platform()), nil

	case "engine.dpmRoot":
		return s.eng.GetDPMRoot(), nil

	case "engine.isInPath":
		return s.eng.IsInPATH(), nil

	case "engine.addToPath":
		if err := s.eng.AddToPATH(); err != nil {
			return nil, &rpcError{Code: errInternal, Message: err.Error()}
		}
		return nil, nil

	case "engine.catalog":
		tools, err := s.eng.Catalog()
		if err != nil {
			return nil, &rpcError{Code: errInternal, Message: err.Error()}
		}
		if tools == nil {
			return []struct{}{}, nil
		}
		return tools, nil

	case "engine.profiles":
		profs, err := s.eng.Profiles()
		if err != nil {
			return nil, &rpcError{Code: errInternal, Message: err.Error()}
		}
		if profs == nil {
			return []struct{}{}, nil
		}
		return profs, nil

	case "engine.dotfiles":
		dfs, err := s.eng.LoadDotfiles()
		if err != nil {
			return nil, &rpcError{Code: errInternal, Message: err.Error()}
		}
		if dfs == nil {
			return []struct{}{}, nil
		}
		return dfs, nil

	case "engine.installed":
		records, err := s.eng.InstalledRecords()
		if err != nil {
			return nil, &rpcError{Code: errInternal, Message: err.Error()}
		}
		if records == nil {
			return []struct{}{}, nil
		}
		return records, nil

	case "engine.checkUpdates":
		statuses, err := s.eng.CheckUpdates()
		if err != nil {
			return nil, &rpcError{Code: errInternal, Message: err.Error()}
		}
		return statuses, nil

	case "engine.installTool":
		var p struct {
			Tool    catalog.Tool        `json:"tool"`
			Version catalog.ToolVersion `json:"version"`
			ToolID  string              `json:"tool_id"`
			VerStr  string              `json:"version_str"`
		}
		if err := decodeParams(params, &p); err != nil {
			return nil, &rpcError{Code: errInvalidParams, Message: err.Error()}
		}
		// Convenience: if frontend only sends an ID we look up the tool.
		if p.Tool.ID == "" && p.ToolID != "" {
			tools, terr := s.eng.Catalog()
			if terr != nil {
				return nil, &rpcError{Code: errInternal, Message: terr.Error()}
			}
			t := catalog.GetToolByID(tools, p.ToolID)
			if t == nil {
				return nil, &rpcError{Code: errInvalidParams, Message: "tool not found: " + p.ToolID}
			}
			p.Tool = *t
			if p.VerStr == "" {
				if v := t.GetDefaultVersion(); v != nil {
					p.Version = *v
				}
			} else {
				for _, v := range t.Versions {
					if v.Version == p.VerStr {
						p.Version = v
						break
					}
				}
			}
		}
		if p.Version.Version == "" {
			return nil, &rpcError{Code: errInvalidParams, Message: "version is required"}
		}
		result, err := s.eng.InstallTool(p.Tool, p.Version)
		if err != nil {
			return nil, &rpcError{Code: errInternal, Message: err.Error()}
		}
		return result, nil

	case "engine.removeTool":
		var p struct {
			ToolID  string `json:"tool_id"`
			Version string `json:"version"`
		}
		if err := decodeParams(params, &p); err != nil {
			return nil, &rpcError{Code: errInvalidParams, Message: err.Error()}
		}
		if p.ToolID == "" || p.Version == "" {
			return nil, &rpcError{Code: errInvalidParams, Message: "tool_id and version are required"}
		}
		if err := s.eng.RemoveTool(p.ToolID, p.Version); err != nil {
			return nil, &rpcError{Code: errInternal, Message: err.Error()}
		}
		return nil, nil

	case "engine.updateTool":
		var p struct {
			ToolID string `json:"tool_id"`
		}
		if err := decodeParams(params, &p); err != nil {
			return nil, &rpcError{Code: errInvalidParams, Message: err.Error()}
		}
		if p.ToolID == "" {
			return nil, &rpcError{Code: errInvalidParams, Message: "tool_id is required"}
		}
		result, err := s.eng.UpdateTool(p.ToolID)
		if err != nil {
			return nil, &rpcError{Code: errInternal, Message: err.Error()}
		}
		return result, nil

	case "engine.applyProfile":
		var p struct {
			Profile profiles.Profile `json:"profile"`
			ID      string           `json:"profile_id"`
		}
		if err := decodeParams(params, &p); err != nil {
			return nil, &rpcError{Code: errInvalidParams, Message: err.Error()}
		}
		if p.Profile.ID == "" && p.ID != "" {
			profs, perr := s.eng.Profiles()
			if perr != nil {
				return nil, &rpcError{Code: errInternal, Message: perr.Error()}
			}
			for _, pp := range profs {
				if pp.ID == p.ID {
					p.Profile = pp
					break
				}
			}
		}
		if p.Profile.ID == "" {
			return nil, &rpcError{Code: errInvalidParams, Message: "profile or profile_id is required"}
		}
		result, err := s.eng.ApplyProfile(p.Profile)
		if err != nil {
			return nil, &rpcError{Code: errInternal, Message: err.Error()}
		}
		return result, nil

	case "engine.installDotfile":
		var p struct {
			Dotfile dotfiles.Dotfile `json:"dotfile"`
			ID      string           `json:"dotfile_id"`
		}
		if err := decodeParams(params, &p); err != nil {
			return nil, &rpcError{Code: errInvalidParams, Message: err.Error()}
		}
		if p.Dotfile.ID == "" && p.ID != "" {
			dfs, derr := s.eng.LoadDotfiles()
			if derr != nil {
				return nil, &rpcError{Code: errInternal, Message: derr.Error()}
			}
			for _, df := range dfs {
				if df.ID == p.ID {
					p.Dotfile = df
					break
				}
			}
		}
		if p.Dotfile.ID == "" {
			return nil, &rpcError{Code: errInvalidParams, Message: "dotfile or dotfile_id is required"}
		}
		result, err := s.eng.InstallDotfile(p.Dotfile)
		if err != nil {
			return nil, &rpcError{Code: errInternal, Message: err.Error()}
		}
		return result, nil

	case "engine.restore":
		result, err := s.eng.Restore()
		if err != nil {
			return nil, &rpcError{Code: errInternal, Message: err.Error()}
		}
		return result, nil

	case "engine.settings.groups":
		mgr := s.eng.GetSettings()
		if mgr == nil {
			return []settings.SettingsGroup{}, nil
		}
		return mgr.Groups(), nil

	case "engine.settings.set":
		var p struct {
			ID    string `json:"id"`
			Value string `json:"value"`
		}
		if err := decodeParams(params, &p); err != nil {
			return nil, &rpcError{Code: errInvalidParams, Message: err.Error()}
		}
		mgr := s.eng.GetSettings()
		if mgr == nil {
			return nil, &rpcError{Code: errInternal, Message: "settings manager not available"}
		}
		if err := mgr.Set(p.ID, p.Value); err != nil {
			return nil, &rpcError{Code: errInternal, Message: err.Error()}
		}
		return nil, nil

	case "engine.settings.toggle":
		var p struct {
			ID string `json:"id"`
		}
		if err := decodeParams(params, &p); err != nil {
			return nil, &rpcError{Code: errInvalidParams, Message: err.Error()}
		}
		mgr := s.eng.GetSettings()
		if mgr == nil {
			return nil, &rpcError{Code: errInternal, Message: "settings manager not available"}
		}
		if err := mgr.Toggle(p.ID); err != nil {
			return nil, &rpcError{Code: errInternal, Message: err.Error()}
		}
		return nil, nil

	case "engine.settings.reset":
		var p struct {
			ID string `json:"id"`
		}
		if err := decodeParams(params, &p); err != nil {
			return nil, &rpcError{Code: errInvalidParams, Message: err.Error()}
		}
		mgr := s.eng.GetSettings()
		if mgr == nil {
			return nil, &rpcError{Code: errInternal, Message: "settings manager not available"}
		}
		if err := mgr.Reset(p.ID); err != nil {
			return nil, &rpcError{Code: errInternal, Message: err.Error()}
		}
		return nil, nil

	case "engine.findProfileByCourseCode":
		var p struct {
			Code string `json:"code"`
		}
		if err := decodeParams(params, &p); err != nil {
			return nil, &rpcError{Code: errInvalidParams, Message: err.Error()}
		}
		if p.Code == "" {
			return nil, &rpcError{Code: errInvalidParams, Message: "code is required"}
		}
		prof, err := s.eng.FindProfileByCourseCode(p.Code)
		if err != nil {
			return nil, &rpcError{Code: errInternal, Message: err.Error()}
		}
		return prof, nil

	case "engine.doctor":
		report, err := s.eng.Doctor()
		if err != nil {
			return nil, &rpcError{Code: errInternal, Message: err.Error()}
		}
		return report, nil

	case "engine.binaryPath":
		var p struct {
			ToolID string `json:"tool_id"`
		}
		if err := decodeParams(params, &p); err != nil {
			return nil, &rpcError{Code: errInvalidParams, Message: err.Error()}
		}
		if p.ToolID == "" {
			return nil, &rpcError{Code: errInvalidParams, Message: "tool_id is required"}
		}
		path, err := s.eng.BinaryPath(p.ToolID)
		if err != nil {
			return nil, &rpcError{Code: errInternal, Message: err.Error()}
		}
		return path, nil

	case "engine.bubble.start":
		session, err := s.eng.BubbleStart()
		if err != nil {
			return nil, &rpcError{Code: errInternal, Message: err.Error()}
		}
		return session, nil

	case "engine.bubble.stop":
		var p struct {
			RootPath string `json:"root_path"`
		}
		if err := decodeParams(params, &p); err != nil {
			return nil, &rpcError{Code: errInvalidParams, Message: err.Error()}
		}
		if p.RootPath == "" {
			return nil, &rpcError{Code: errInvalidParams, Message: "root_path is required"}
		}
		if err := s.eng.BubbleStop(p.RootPath); err != nil {
			return nil, &rpcError{Code: errInternal, Message: err.Error()}
		}
		return nil, nil

	case "engine.dotfiles.scan":
		var p struct {
			RepoURL string `json:"repo_url"`
		}
		if err := decodeParams(params, &p); err != nil {
			return nil, &rpcError{Code: errInvalidParams, Message: err.Error()}
		}
		if p.RepoURL == "" {
			return nil, &rpcError{Code: errInvalidParams, Message: "repo_url is required"}
		}
		result, err := s.eng.ScanGitDotfiles(p.RepoURL)
		if err != nil {
			return nil, &rpcError{Code: errInternal, Message: err.Error()}
		}
		return result, nil

	case "engine.dotfiles.planImported":
		var p struct {
			RepoDir string                        `json:"repo_dir"`
			Configs []engine.ScannedDotfileConfig `json:"configs"`
		}
		if err := decodeParams(params, &p); err != nil {
			return nil, &rpcError{Code: errInvalidParams, Message: err.Error()}
		}
		if p.RepoDir == "" {
			return nil, &rpcError{Code: errInvalidParams, Message: "repo_dir is required"}
		}
		result, err := s.eng.PlanImportedDotfiles(p.RepoDir, p.Configs)
		if err != nil {
			return nil, &rpcError{Code: errInternal, Message: err.Error()}
		}
		return result, nil

	case "engine.dotfiles.applyImported":
		var p struct {
			RepoDir string                        `json:"repo_dir"`
			Configs []engine.ScannedDotfileConfig `json:"configs"`
		}
		if err := decodeParams(params, &p); err != nil {
			return nil, &rpcError{Code: errInvalidParams, Message: err.Error()}
		}
		if p.RepoDir == "" {
			return nil, &rpcError{Code: errInvalidParams, Message: "repo_dir is required"}
		}
		result, err := s.eng.ApplyImportedDotfiles(p.RepoDir, p.Configs)
		if err != nil {
			return nil, &rpcError{Code: errInternal, Message: err.Error()}
		}
		return result, nil

	default:
		return nil, &rpcError{Code: errMethodNotFound, Message: "unknown method: " + method}
	}
}

// decodeParams unmarshals params into target. An empty (null/missing) params
// is treated as `{}` so callers don't have to special-case methods with no
// arguments.
func decodeParams(raw json.RawMessage, target any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, target)
}

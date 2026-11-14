package installer

import (
	"bufio"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSystem records commands and pretends to be a fresh Ubuntu Server.
type fakeSystem struct {
	mu        sync.Mutex
	cmds      []string
	installed map[string]bool // binaries LookPath finds
	ufwActive bool
	libraries []string // paths POSTed to the fake RCM
	healthy   bool
}

func (f *fakeSystem) ran(prefix string) bool {
	for _, c := range f.cmds {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func newEnv(t *testing.T, input string) (*Env, *fakeSystem, *strings.Builder) {
	t.Helper()
	root := t.TempDir()
	me, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	fs := &fakeSystem{installed: map[string]bool{"apt-get": true, "runuser": true, "ufw": true}, healthy: true}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/status" && fs.healthy:
			w.Write([]byte(`{}`))
		case r.URL.Path == "/api/libraries" && r.Method == "POST":
			var body struct{ Path string }
			json.NewDecoder(r.Body).Decode(&body)
			fs.mu.Lock()
			fs.libraries = append(fs.libraries, body.Path)
			fs.mu.Unlock()
			w.WriteHeader(201)
		default:
			w.WriteHeader(503)
		}
	}))
	t.Cleanup(srv.Close)

	exe := filepath.Join(t.TempDir(), "rcm-linux-amd64")
	os.WriteFile(exe, []byte("NEW BINARY"), 0o755)
	out := &strings.Builder{}
	e := &Env{
		Root: root,
		In:   bufio.NewReader(strings.NewReader(input)),
		Out:  out,
		Run: func(name string, args ...string) (string, error) {
			fs.mu.Lock()
			defer fs.mu.Unlock()
			cmd := strings.Join(append([]string{name}, args...), " ")
			fs.cmds = append(fs.cmds, cmd)
			switch {
			case name == "ufw" && len(args) > 0 && args[0] == "status":
				if fs.ufwActive {
					return "Status: active\n", nil
				}
				return "Status: inactive\n", nil
			case name == "lsblk":
				return "NAME SIZE FSTYPE LABEL MOUNTPOINT\nsdb1 4T ext4 Big /media/big drive\n", nil
			case name == "apt-get" && len(args) > 0 && args[0] == "install":
				fs.installed["ffprobe"], fs.installed["avahi-daemon"] = true, true
			}
			return "", nil
		},
		LookPath: func(name string) (string, error) {
			if fs.installed[name] {
				return "/usr/bin/" + name, nil
			}
			return "", errors.New("not found")
		},
		LookupUser: func(name string) (*user.User, error) {
			if name == "wolf" {
				return &user.User{Username: "wolf", Uid: me.Uid, Gid: me.Gid, HomeDir: "/home/wolf"}, nil
			}
			return nil, user.UnknownUserError(name)
		},
		PortFree:   func(port int) bool { return port != 9999 },
		BaseURL:    func(int) string { return srv.URL },
		Hostname:   func() (string, error) { return "mini", nil },
		Getenv:     func(k string) string { return map[string]string{"SUDO_USER": "wolf"}[k] },
		Sleep:      func(time.Duration) {},
		Executable: exe,
		Version:    "test",
		IsRoot:     true,
		HasSystemd: true,
		HTTP:       srv.Client(),
	}
	return e, fs, out
}

func read(t *testing.T, e *Env, abs string) string {
	t.Helper()
	b, err := os.ReadFile(e.p(abs))
	if err != nil {
		t.Fatalf("read %s: %v", abs, err)
	}
	return string(b)
}

func TestFreshInstallNonInteractive(t *testing.T) {
	e, fs, out := newEnv(t, "")
	fs.ufwActive = true
	movies := "/media/big drive/Movies & 100% Fun"
	music := "/srv/music"
	os.MkdirAll(e.p(movies), 0o755)
	os.MkdirAll(e.p(music), 0o755)

	err := e.Install(Options{Yes: true, Media: []string{movies, music, "/media/not-mounted", "relative/path"}})
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}

	if got := read(t, e, BinPath); got != "NEW BINARY" {
		t.Errorf("binary = %q", got)
	}
	if fi, _ := os.Stat(e.p(BinPath)); fi.Mode().Perm() != 0o755 {
		t.Errorf("binary mode %v", fi.Mode().Perm())
	}
	unit := read(t, e, UnitPath)
	wantExec := `ExecStart=/usr/local/bin/rcm -addr :8080 -data "/var/lib/rcm" -import "/media/big drive/Movies & 100%% Fun/RCM Imports"`
	if !strings.Contains(unit, wantExec) {
		t.Errorf("unit ExecStart wrong:\n%s\nwant line: %s", unit, wantExec)
	}
	if !strings.Contains(unit, "User=wolf\n") || !strings.Contains(unit, "WantedBy=multi-user.target") {
		t.Errorf("unit missing user or install section:\n%s", unit)
	}
	if fi, err := os.Stat(e.p("/media/big drive/Movies & 100% Fun/RCM Imports")); err != nil || !fi.IsDir() {
		t.Error("import folder not created")
	}
	if fi, err := os.Stat(e.p(DataDir)); err != nil || !fi.IsDir() {
		t.Error("data dir not created")
	}
	if !strings.Contains(read(t, e, SysctlPath), "max_user_watches=524288") {
		t.Error("sysctl file wrong")
	}
	c := parseConfig(read(t, e, ConfPath))
	if c.User != "wolf" || c.Port != 8080 || len(c.Media) != 2 || c.Media[0] != movies {
		t.Errorf("config = %+v", c)
	}
	for _, want := range []string{
		"apt-get update", "apt-get install -y -q ffmpeg avahi-daemon", "systemctl enable --now avahi-daemon",
		"sysctl -q -p " + SysctlPath, "ufw allow 8080/tcp", "systemctl daemon-reload", "systemctl enable rcm", "systemctl restart rcm",
	} {
		if !fs.ran(want) {
			t.Errorf("expected command %q; ran:\n%s", want, strings.Join(fs.cmds, "\n"))
		}
	}
	if strings.Join(fs.libraries, "|") != movies+"|"+music {
		t.Errorf("libraries added = %v", fs.libraries)
	}
	text := out.String()
	for _, want := range []string{"not-mounted isn't a folder", "isn't a full path", "http://mini.local:8080", "All set!"} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q:\n%s", want, text)
		}
	}
}

func TestInteractiveInstallUsesDriveSuggestions(t *testing.T) {
	// answers: pick suggestion 1, type a path, finish; accept import default; port 8090;
	// accept the summary; accept installing packages; accept opening the firewall
	e, fs, out := newEnv(t, "1\n/srv/books\n\n\n8090\ny\n\n\n")
	fs.ufwActive = true
	os.MkdirAll(e.p("/proc"), 0o755)
	os.WriteFile(e.p("/proc/mounts"), []byte("/dev/sda2 / ext4 rw 0 0\n/dev/sdb1 /media/big\\040drive ext4 rw 0 0\nproc /proc proc rw 0 0\n"), 0o644)
	os.MkdirAll(e.p("/media/big drive"), 0o755)
	os.MkdirAll(e.p("/srv/books"), 0o755)

	if err := e.Install(Options{}); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if os.Getenv("RCM_SHOW_TRANSCRIPT") != "" {
		t.Log("\n" + out.String())
	}
	c := parseConfig(read(t, e, ConfPath))
	if strings.Join(c.Media, "|") != "/media/big drive|/srv/books" || c.Port != 8090 || c.ImportDir != "/media/big drive/RCM Imports" {
		t.Errorf("config = %+v\n%s", c, out)
	}
	if !strings.Contains(out.String(), "1) /media/big drive") || !strings.Contains(out.String(), "sdb1 4T ext4") {
		t.Errorf("drive list not shown:\n%s", out)
	}
	if !fs.ran("ufw allow 8090/tcp") {
		t.Error("firewall not opened for the chosen port")
	}
}

func TestCancelChangesNothing(t *testing.T) {
	e, fs, _ := newEnv(t, "\n\n\nn\n") // no media, default import, default port, then "no"
	if err := e.Install(Options{}); !errors.Is(err, errCancelled) {
		t.Fatalf("got %v, want cancelled", err)
	}
	if _, err := os.Stat(e.p(BinPath)); err == nil {
		t.Error("binary installed after cancel")
	}
	if len(fs.cmds) > 1 { // only lsblk may have run
		t.Errorf("commands ran after cancel: %v", fs.cmds)
	}
}

func TestUpgradeKeepsSettings(t *testing.T) {
	e, fs, out := newEnv(t, "")
	old := Config{User: "wolf", Port: 8123, ImportDir: "/srv/imports", Media: []string{"/srv/media"}}
	os.MkdirAll(e.p(filepath.Dir(ConfPath)), 0o755)
	os.WriteFile(e.p(ConfPath), []byte(old.marshal()), 0o644)
	os.MkdirAll(e.p(filepath.Dir(BinPath)), 0o755)
	os.WriteFile(e.p(BinPath), []byte("OLD BINARY"), 0o755)

	if err := e.Install(Options{}); err != nil {
		t.Fatalf("upgrade: %v\n%s", err, out)
	}
	if got := read(t, e, BinPath); got != "NEW BINARY" {
		t.Errorf("binary not replaced: %q", got)
	}
	if !strings.Contains(read(t, e, UnitPath), "-addr :8123") {
		t.Error("upgrade lost the port")
	}
	if !fs.ran("systemctl restart rcm") || fs.ran("apt-get") || len(fs.libraries) != 0 {
		t.Errorf("upgrade did the wrong things: %v libs=%v", fs.cmds, fs.libraries)
	}
	if !strings.Contains(out.String(), "Upgraded to test") {
		t.Errorf("output:\n%s", out)
	}
}

func TestRefusals(t *testing.T) {
	e, _, _ := newEnv(t, "")
	e.IsRoot = false
	if err := e.Install(Options{Yes: true}); err == nil || !strings.Contains(err.Error(), "sudo") {
		t.Errorf("non-root: %v", err)
	}
	e, _, _ = newEnv(t, "")
	if err := e.Install(Options{Yes: true, Port: 9999}); err == nil || !strings.Contains(err.Error(), "in use") {
		t.Errorf("busy port: %v", err)
	}
	e, _, _ = newEnv(t, "")
	e.Getenv = func(string) string { return "" }
	if err := e.Install(Options{Yes: true}); err == nil || !strings.Contains(err.Error(), "--user") {
		t.Errorf("unknown account: %v", err)
	}
	e, _, _ = newEnv(t, "")
	if err := e.Install(Options{Yes: true, User: "root"}); err == nil || !strings.Contains(err.Error(), "root") {
		t.Errorf("root account: %v", err)
	}
	e, fs, _ := newEnv(t, "")
	fs.healthy = false
	if err := e.Install(Options{Yes: true}); err == nil || !strings.Contains(err.Error(), "didn't respond") {
		t.Errorf("unhealthy service: %v", err)
	}
}

func TestUninstall(t *testing.T) {
	e, fs, out := newEnv(t, "")
	os.MkdirAll(e.p("/srv/media"), 0o755)
	if err := e.Install(Options{Yes: true, Media: []string{"/srv/media"}}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(e.p(DataDir+"/rcm.db"), []byte("db"), 0o644)
	os.WriteFile(e.p("/srv/media/RCM Imports/keep.mkv"), []byte("x"), 0o644)

	if err := e.Uninstall(Options{Yes: true}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{BinPath, UnitPath, ConfPath, SysctlPath} {
		if _, err := os.Stat(e.p(p)); err == nil {
			t.Errorf("%s still exists", p)
		}
	}
	if _, err := os.Stat(e.p(DataDir + "/rcm.db")); err != nil {
		t.Error("database removed without --purge")
	}
	if _, err := os.Stat(e.p("/srv/media/RCM Imports/keep.mkv")); err != nil {
		t.Error("media was touched")
	}
	if !fs.ran("systemctl disable --now rcm") {
		t.Error("service not stopped")
	}

	if err := e.Uninstall(Options{Yes: true, Purge: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.p(DataDir)); err == nil {
		t.Errorf("data dir kept with --purge\n%s", out)
	}
	if _, err := os.Stat(e.p("/srv/media/RCM Imports/keep.mkv")); err != nil {
		t.Error("purge touched media")
	}
}

func TestSdQuote(t *testing.T) {
	for in, want := range map[string]string{
		`/media/x`:          `"/media/x"`,
		`/m/My "Big" Drive`: `"/m/My \"Big\" Drive"`,
		`/m/100%/$HOME\x`:   `"/m/100%%/$$HOME\\x"`,
	} {
		if got := sdQuote(in); got != want {
			t.Errorf("sdQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestConfigRoundTrip(t *testing.T) {
	c := Config{User: "wolf", Port: 8080, ImportDir: "/a b/c=d", Media: []string{"/x y", "/z=1"}}
	if got := parseConfig(c.marshal()); got.User != c.User || got.Port != c.Port || got.ImportDir != c.ImportDir ||
		strings.Join(got.Media, "|") != strings.Join(c.Media, "|") {
		t.Errorf("round trip: %+v", got)
	}
}

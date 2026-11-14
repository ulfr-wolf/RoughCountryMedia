// Command rcm is the Rough Country Media server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"rcm/internal/installer"
	"rcm/internal/library"
	"rcm/internal/server"
	"rcm/web"
)

var version = "dev"

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "install" || os.Args[1] == "uninstall") {
		os.Exit(installer.Main(os.Args[1:], version))
	}
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Rough Country Media %s\n\n"+
			"  rcm [flags]            run the server\n"+
			"  sudo rcm install       set RCM up as a service on Linux (re-run to upgrade)\n"+
			"  sudo rcm uninstall     remove the service (your media is never touched)\n\nServer flags:\n", version)
		flag.PrintDefaults()
	}
	addr := flag.String("addr", ":8080", "address to listen on")
	dataDir := flag.String("data", "./rcm-data", "folder for the database (and default import folder)")
	importDir := flag.String("import", "", "folder where drag-and-drop imports are saved (default <data>/media)")
	rescan := flag.Duration("rescan", 6*time.Hour, "full rescan interval as a safety net (0 disables)")
	var libs multiFlag
	flag.Var(&libs, "lib", "media folder to add as a library (repeatable; remembered)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("rcm", version)
		return
	}

	data, err := filepath.Abs(*dataDir)
	if err != nil {
		log.Fatal(err)
	}
	imp := *importDir
	if imp == "" {
		imp = filepath.Join(data, "media")
	}
	if imp, err = filepath.Abs(imp); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(imp, 0o755); err != nil {
		log.Fatalf("import folder: %v", err)
	}

	store, err := library.OpenStore(filepath.Join(data, "rcm.db"))
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer store.Close()

	for _, p := range libs {
		if _, err := store.AddLibrary(p); err != nil {
			log.Printf("library %s: %v", p, err)
		}
	}
	// The import folder is its own library unless an existing library already contains it.
	if _, covered := store.LibraryFor(imp); !covered {
		if _, err := store.AddLibrary(imp); err != nil {
			log.Printf("import folder %s: %v", imp, err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mgr := library.NewManager(store)
	mgr.Start(ctx, *rescan)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           server.New(mgr, imp, filepath.Join(data, "cache"), web.FS()).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// No read/write timeouts: movie streams and big uploads run for hours.
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()

	log.Printf("Rough Country Media %s — data %s, imports %s", version, data, imp)
	for _, u := range lanURLs(*addr) {
		log.Printf("open %s", u)
	}
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	log.Print("stopped")
}

// lanURLs lists the addresses people on the network can open.
func lanURLs(addr string) []string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil
	}
	if host != "" && host != "0.0.0.0" && host != "::" {
		return []string{"http://" + net.JoinHostPort(host, port)}
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		return nil
	}
	name, _ := os.Hostname()
	return installer.LANURLs(name, p)
}

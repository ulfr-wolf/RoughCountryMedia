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
	"strings"
	"syscall"
	"time"

	"rcm/internal/library"
	"rcm/internal/server"
	"rcm/web"
)

var version = "dev"

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func main() {
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
	urls := []string{"http://localhost:" + port}
	if name, err := os.Hostname(); err == nil {
		name = strings.TrimSuffix(name, ".local")
		urls = append(urls, "http://"+name+".local:"+port)
	}
	ifaces, _ := net.InterfaceAddrs()
	for _, a := range ifaces {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLoopback() && !ipn.IP.IsLinkLocalUnicast() {
			urls = append(urls, "http://"+ipn.IP.String()+":"+port)
		}
	}
	return urls
}

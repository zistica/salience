package cli

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	"github.com/salience-cli/salience/internal/server"
	"github.com/salience-cli/salience/internal/store"
)

// RunServe boots the local dashboard server.
func RunServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	dbPath := fs.String("db", "salience.db", "path to SQLite database")
	port := fs.Int("port", 7878, "TCP port to listen on")
	bind := fs.String("bind", "127.0.0.1", "interface to bind to (use 0.0.0.0 to allow LAN access — there's no auth, do this only on trusted networks)")
	open := fs.Bool("open", true, "open the dashboard in the default browser on startup")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, err := os.Stat(*dbPath); os.IsNotExist(err) {
		return fmt.Errorf("no database at %s — run `salience bench` first", *dbPath)
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	addr := *bind + ":" + strconv.Itoa(*port)
	srv := server.New(st, *dbPath)
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	url := "http://" + addr + "/"
	fmt.Printf("Salience dashboard listening on %s (DB %s)\n", url, *dbPath)
	if *bind == "127.0.0.1" {
		fmt.Println("Bound to localhost only; pass -bind 0.0.0.0 to expose on the LAN (no auth).")
	}
	if *open {
		openBrowser(url)
	}

	// Graceful shutdown on Ctrl-C.
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx)
	}()

	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return
	}
	_ = cmd.Start()
}

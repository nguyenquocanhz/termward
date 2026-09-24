// Command termwardd is the Termward core: it keeps SSH connections, the key
// store and the health monitor, and serves the desktop UI on 127.0.0.1.
//
// The desktop app starts it with TERMWARD_TOKEN set and reads the chosen port
// from the first stdout line ("TERMWARD_READY <port>"). When the parent closes
// stdin (or dies) the core exits, so it never outlives the app.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/nguyenquocanhz/termward/core/internal/app"
)

var version = "dev"

func main() {
	var (
		dataDir     = flag.String("data-dir", defaultDataDir(), "where hosts, keys and known_hosts are stored")
		addr        = flag.String("addr", "127.0.0.1:0", "listen address (keep it on loopback)")
		dev         = flag.Bool("dev", false, "development mode: port 7717 and token \"dev\" unless overridden")
		watchStdin  = flag.Bool("watch-stdin", false, "exit when stdin is closed (used by the desktop app)")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("termwardd: ")
	log.SetOutput(os.Stderr)

	token := os.Getenv("TERMWARD_TOKEN")
	if *dev {
		if token == "" {
			token = "dev"
		}
		if *addr == "127.0.0.1:0" {
			*addr = "127.0.0.1:7717"
		}
	}
	if token == "" {
		token = randomToken()
		log.Printf("no TERMWARD_TOKEN given, generated one: %s", token)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if *watchStdin {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		go func() {
			_, _ = io.Copy(io.Discard, os.Stdin)
			cancel()
		}()
	}

	var extraKnown []string
	if home, err := os.UserHomeDir(); err == nil {
		extraKnown = append(extraKnown, filepath.Join(home, ".ssh", "known_hosts"))
	}
	inst, err := app.Start(ctx, app.Config{
		DataDir:         *dataDir,
		Addr:            *addr,
		Token:           token,
		Version:         version,
		ExtraKnownHosts: extraKnown,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("TERMWARD_READY %d\n", inst.Port)
	log.Printf("listening on 127.0.0.1:%d, data in %s", inst.Port, *dataDir)

	select {
	case <-ctx.Done():
	case <-inst.Done():
	}
	inst.Stop()
}

func defaultDataDir() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "Termward", "core")
	}
	return ".termward"
}

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

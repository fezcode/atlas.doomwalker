package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"

	"atlas.doomwalker/internal/mft"
	"atlas.doomwalker/internal/ui"
	"atlas.doomwalker/internal/walker"
	"atlas.doomwalker/internal/web"
	tea "github.com/charmbracelet/bubbletea"
)

var Version = "dev"

func main() {
	defer func() {
		if r := recover(); r != nil {
			errStr := fmt.Sprintf("Panic: %v\n\n%s", r, string(debug.Stack()))
			os.WriteFile("doomwalker_crash.log", []byte(errStr), 0644)
			fmt.Println("A fatal error occurred. Check doomwalker_crash.log for details.")
			os.Exit(1)
		}
	}()

	args := filterArgs(os.Args[1:])

	var (
		showVersion  bool
		showHelp     bool
		serve        bool
		addr         string
		forceWalker  bool
	)
	fs := flag.NewFlagSet("atlas.doomwalker", flag.ContinueOnError)
	fs.BoolVar(&showVersion, "v", false, "show version")
	fs.BoolVar(&showVersion, "version", false, "show version")
	fs.BoolVar(&showHelp, "h", false, "show help")
	fs.BoolVar(&showHelp, "help", false, "show help")
	fs.BoolVar(&serve, "serve", false, "serve a browser-based UI instead of the TUI")
	fs.BoolVar(&forceWalker, "walker", false, "force the cross-platform walker (skip MFT)")
	fs.StringVar(&addr, "addr", "127.0.0.1:7878", "address for --serve")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if showVersion {
		fmt.Printf("atlas.doomwalker v%s\n", Version)
		return
	}
	if showHelp {
		printHelp(fs)
		return
	}

	target := defaultTarget()
	if rest := fs.Args(); len(rest) > 0 {
		target = rest[0]
	}

	useMFT := runtime.GOOS == "windows" && !forceWalker
	if useMFT && !isAdmin() {
		if elevated() {
			fmt.Println("Elevation appears to have failed. Run as Administrator manually,")
			fmt.Println("or pass --walker to use the slower cross-platform scanner.")
			os.Exit(1)
		}
		fmt.Println("MFT scanning needs Administrator. Attempting to relaunch elevated…")
		fmt.Println("(or pass --walker to skip MFT and use the portable walker.)")
		if err := elevate(); err != nil {
			fmt.Printf("Could not relaunch: %v\n", err)
			os.Exit(1)
		}
		return
	}

	scan := func(pChan chan<- any) (*mft.FileNode, error) {
		if useMFT {
			return mft.NewScanner(target).Scan(pChan)
		}
		return walker.Scan(target, pChan)
	}

	if serve {
		runServe(target, addr, scan)
		return
	}
	runTUI(scan)
}

func runTUI(scan func(chan<- any) (*mft.FileNode, error)) {
	model := ui.NewModel()
	p := tea.NewProgram(model, tea.WithAltScreen())

	go func() {
		pChan := make(chan any, 8)
		done := make(chan struct{})
		go func() {
			for v := range pChan {
				switch x := v.(type) {
				case float64:
					p.Send(ui.ProgressMsg(x))
				case string:
					p.Send(ui.StatusMsg(x))
				}
			}
			close(done)
		}()

		root, err := scan(pChan)
		close(pChan)
		<-done

		if err != nil {
			p.Send(ui.ScanErrorMsg{Err: err})
			return
		}
		p.Send(ui.ScanFinishedMsg{Root: root})
	}()

	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}

func runServe(target, addr string, scan func(chan<- any) (*mft.FileNode, error)) {
	fmt.Printf("Scanning %s …\n", target)
	pChan := make(chan any, 8)
	go func() {
		for v := range pChan {
			switch x := v.(type) {
			case float64:
				fmt.Printf("\r  %5.1f%%", x*100)
			case string:
				fmt.Printf("\n  %s", x)
			}
		}
	}()
	root, err := scan(pChan)
	close(pChan)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nScan failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("\nScan complete. Starting server.")
	if err := web.Serve(addr, root); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

func printHelp(fs *flag.FlagSet) {
	fmt.Println("atlas.doomwalker — disk space analyzer")
	fmt.Println()
	fmt.Println("Uses the NTFS Master File Table on Windows for fast scans, falls back")
	fmt.Println("to a portable filesystem walker on Linux/macOS or with --walker.")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  atlas.doomwalker [flags] [drive|path]")
	fmt.Println()
	fmt.Println("Flags:")
	fs.PrintDefaults()
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  atlas.doomwalker                # scan default target")
	fmt.Println("  atlas.doomwalker D:             # scan D: with MFT (Windows, admin)")
	fmt.Println("  atlas.doomwalker --walker /home # walk /home cross-platform")
	fmt.Println("  atlas.doomwalker --serve        # browser UI on http://127.0.0.1:7878")
}

func filterArgs(in []string) []string {
	out := in[:0:len(in)]
	for _, a := range in {
		if a == "--elevated" {
			continue
		}
		out = append(out, a)
	}
	return out
}

func elevated() bool {
	for _, a := range os.Args {
		if a == "--elevated" {
			return true
		}
	}
	return false
}

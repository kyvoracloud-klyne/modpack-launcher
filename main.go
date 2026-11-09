package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"modpack-launcher/internal/app"
	"modpack-launcher/internal/config"
	"modpack-launcher/internal/game"
	"modpack-launcher/internal/githubrel"
	"modpack-launcher/internal/ui"
)

func cliProgress(p githubrel.Progress) {
	fmt.Printf("\r%s\033[K", githubrel.FormatProgress(p))
	if p.Done {
		fmt.Println()
	}
}

// Version is set with -ldflags "-X main.Version=...".
var Version = "dev"

func main() {
	if len(os.Args) < 2 {
		if err := cmdGUI(nil); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}
	var err error
	switch os.Args[1] {
	case "gui":
		err = cmdGUI(os.Args[2:])
	case "update":
		err = cmdUpdate(os.Args[2:])
	case "packs":
		err = cmdPacks(os.Args[2:])
	case "launch":
		err = cmdLaunch(os.Args[2:])
	case "restore":
		err = cmdRestore(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Printf(`modpack-launcher %s

Usage:
  modpack-launcher
  modpack-launcher gui
  modpack-launcher update
  modpack-launcher packs
  modpack-launcher launch --pack ID [--version V] [--pack-dir PATH]
  modpack-launcher restore

With no arguments, the pack window opens. Each pack is listed once.
The description page has a version menu.

update downloads the newest launcher release and replaces this binary.
The launcher repository is kyvoracloud-klyne/modpack-launcher.
Pack updates come from kyvoracloud-klyne/cosmoteer-modpack-2026 release
manifest.json. A release can name one archive or ordered parts that join
into that archive.
launch downloads the chosen pack, copies missing Saved Ships and Lost Ships
into <game>/ModPackProfile, swaps Standard Mods and the YAML Bin proxy in,
enables and trusts those mods, and starts Cosmoteer with --rootsavepath.
Career saves are not copied. The pack stays installed after Cosmoteer closes.
restore uninstalls it, putting Standard Mods and the Bin proxy back.

On Linux the game is started with steam -applaunch %s. On Windows,
Bin/Cosmoteer.exe is started directly.

Config: %%APPDATA%% or ~/.config/%s/config.json
  {"launcherRepo":"owner/name","packRepo":"owner/name","gameRoot":""}
GitHub token: MODPACK_GITHUB_TOKEN or GITHUB_TOKEN (not stored in the config file).
`, Version, game.AppID, config.AppName)
}

type common struct {
	configPath   string
	launcherRepo string
	packRepo     string
	gameRoot     string
}

func (c *common) add(fs *flag.FlagSet) {
	fs.StringVar(&c.configPath, "config", "", "config file (default: the user config directory)")
	fs.StringVar(&c.launcherRepo, "launcher-repo", "", "GitHub owner/name for launcher releases")
	fs.StringVar(&c.packRepo, "pack-repo", "", "GitHub owner/name for pack releases")
	fs.StringVar(&c.gameRoot, "game", "", "Cosmoteer install folder")
}

func (c *common) load() (config.Config, error) {
	cfg, err := config.Load(c.configPath)
	if err != nil {
		return cfg, err
	}
	config.Apply(&cfg, c.launcherRepo, c.packRepo, c.gameRoot)
	config.FillDefaults(&cfg)
	return cfg, nil
}

func cmdGUI(args []string) error {
	fs := flag.NewFlagSet("gui", flag.ContinueOnError)
	common := &common{}
	common.add(fs)
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	cfg, err := common.load()
	if err != nil {
		return err
	}
	if err := app.Recover(nil); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}
	server := ui.New(Version, common.configPath, cfg, nil)
	go server.RefreshPacks()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	httpServer := &http.Server{Handler: server.Handler()}
	go func() { _ = httpServer.Serve(listener) }()
	url := "http://" + listener.Addr().String()
	fmt.Printf("Pack window: %s\n", url)
	if err := ui.Open(url); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: open a browser to %s (%v)\n", url, err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go server.Watch(ctx)
	<-server.Done()
	_ = httpServer.Close()
	return nil
}

func cmdUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	common := &common{}
	common.add(fs)
	check := fs.Bool("check", false, "print the latest release and do not replace this binary")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	cfg, err := common.load()
	if err != nil {
		return err
	}
	exit, err := app.Update(cfg, Version, *check, nil, cliProgress)
	if exit {
		os.Exit(0)
	}
	return err
}

func cmdPacks(args []string) error {
	fs := flag.NewFlagSet("packs", flag.ContinueOnError)
	common := &common{}
	common.add(fs)
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	cfg, err := common.load()
	if err != nil {
		return err
	}
	if err := app.Recover(nil); err != nil {
		return err
	}
	packs, err := app.List(context.Background(), cfg)
	if err != nil {
		return err
	}
	if len(packs) == 0 {
		fmt.Println("No packs found. A release needs manifest.json and the archive it names.")
		return nil
	}
	for _, item := range packs {
		title := item.Manifest.Name
		if title == "" {
			title = item.Manifest.ID
		}
		fmt.Printf("%s\t%s\t%s\t%s\n", item.Manifest.ID, item.Manifest.Version, item.Manifest.Archive, title)
	}
	return nil
}

func cmdLaunch(args []string) error {
	fs := flag.NewFlagSet("launch", flag.ContinueOnError)
	common := &common{}
	common.add(fs)
	packID := fs.String("pack", "", "pack id from the pack repository")
	version := fs.String("version", "", "pack version (default: newest)")
	packDir := fs.String("pack-dir", "", "use this extracted pack instead of downloading")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	cfg, err := common.load()
	if err != nil {
		return err
	}
	return app.Launch(cfg, *packID, *version, *packDir, nil, cliProgress)
}

func cmdRestore(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	common := &common{}
	common.add(fs)
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if _, err := common.load(); err != nil {
		return err
	}
	if err := app.Restore(); err != nil {
		return err
	}
	fmt.Println("Uninstalled the mod pack")
	return nil
}

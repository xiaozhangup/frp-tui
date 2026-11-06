package main

import (
	"flag"
	"fmt"
	"os"

	"frp-tui/internal/daemon"
	"frp-tui/internal/instance"
	"frp-tui/internal/ui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		os.Exit(1)
	}
}

func run() error {
	args := os.Args[1:]
	mode := "tui"
	if len(args) > 0 && args[0] == "daemon" {
		mode = "daemon"
		args = args[1:]
		if len(args) > 0 && args[0] == "stop" {
			mode = "stop"
			args = args[1:]
		}
	}
	flags := flag.NewFlagSet("frp-tui", flag.ContinueOnError)
	dataDir := flags.String("data-dir", "", "数据目录，默认使用程序所在目录")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "用法：frp-tui [--data-dir 目录]\n      frp-tui daemon [--data-dir 目录]\n      frp-tui daemon stop [--data-dir 目录]\n\nF10 关闭界面，连接继续运行；F12 停止所有 frpc、守护进程并退出。\n鼠标与 Tab 可操作界面。")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("未知参数：%s", flags.Arg(0))
	}
	i, err := instance.New(*dataDir)
	if err != nil {
		return err
	}
	switch mode {
	case "daemon":
		return daemon.Run(i.DataDir, i.Socket)
	case "stop":
		return i.Shutdown()
	default:
		if err := i.Ensure(); err != nil {
			return err
		}
		return ui.Run(i.Client, i.Reconnect, i.Shutdown)
	}
}

package main

import (
	"GoMuxProxy/gui"
	"GoMuxProxy/proxy"
	"log"
	"net"
	"os"
	"time"

	"fyne.io/fyne/v2/app"
)

const lockAddr = "127.0.0.1:48321"

func tryLock() (net.Listener, bool) {
	ln, err := net.Listen("tcp", lockAddr)
	if err != nil {
		return nil, false
	}
	return ln, true
}

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	// 单实例检测：尝试监听一个固定端口，失败说明已有实例在运行
	ln, ok := tryLock()
	if !ok {
		log.Println("GoMuxProxy 已在运行中，不能重复启动")
		os.Exit(0)
	}
	defer ln.Close()

	cfgPath := "config.json"
	cfg := proxy.Config{
		UpstreamProxy: "127.0.0.1:10810",
		Listeners: []proxy.ListenEntry{
			{Network: "tcp", Address: "127.0.0.1:1081", Protocol: "mixed", Enabled: true},
			{Network: "tcp", Address: "127.0.0.1:1082", Protocol: "mixed", Enabled: true},
			{Network: "tcp", Address: "127.0.0.1:1083", Protocol: "mixed", Enabled: true},
		},
	}

	if loaded, err := proxy.LoadConfig(cfgPath); err == nil {
		cfg = loaded
	} else if !os.IsNotExist(err) {
		log.Printf("配置加载错误: %v，使用默认配置", err)
	}

	engine := proxy.NewEngine(cfg)

	fyneApp := app.New()

	guiApp := gui.NewApp(fyneApp, engine)

	// 保持锁监听，防止被 GC
	go func() {
		for {
			ln.Accept()
			time.Sleep(time.Second)
		}
	}()

	guiApp.Run()
}

package main

import (
	"context"
	"flag"
	"log"
	"strings"

	"lisanalgaib/internal/extensionhost"
	"lisanalgaib/internal/lifecycle"
)

func main() {
	listen := flag.String("listen", ":7777", "HTTP listen address")
	configPath := flag.String("config", "", "required extension configuration path")
	flag.Parse()
	if strings.TrimSpace(*configPath) == "" {
		log.Fatal("--config is required")
	}
	ctx, stop := lifecycle.NotifyContext(context.Background())
	defer stop()
	if err := extensionhost.Serve(ctx, *listen, *configPath, log.Default()); err != nil {
		log.Fatal(err)
	}
}

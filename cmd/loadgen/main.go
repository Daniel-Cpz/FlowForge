package main

import (
	"context"
	"encoding/json"
	"flag"
	"github.com/Daniel-Cpz/FlowForge/internal/loadgen"
	"os"
	"os/signal"
	"time"
)

func main() {
	c := loadgen.Config{}
	flag.StringVar(&c.BaseURL, "url", "http://127.0.0.1:8080", "API base URL")
	flag.IntVar(&c.Count, "count", 500, "Jobs")
	flag.IntVar(&c.Concurrency, "concurrency", 16, "HTTP concurrency (1..128)")
	flag.IntVar(&c.DurationMS, "duration-ms", 25, "SLEEP duration")
	flag.DurationVar(&c.MaxWait, "max-wait", 5*time.Minute, "whole-run deadline")
	flag.BoolVar(&c.Idempotent, "idempotent", false, "use unique per-run keys")
	flag.Parse()
	ctx, end := signal.NotifyContext(context.Background(), os.Interrupt)
	defer end()
	s, e := loadgen.Run(ctx, c)
	json.NewEncoder(os.Stdout).Encode(s)
	if e != nil {
		os.Stderr.WriteString("loadgen incomplete or invalid configuration\n")
		os.Exit(1)
	}
}

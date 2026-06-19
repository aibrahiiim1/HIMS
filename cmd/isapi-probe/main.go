// Command isapi-probe is a throwaway latency diagnostic: it runs the real
// internal/isapi collector against one recorder and prints the wall-clock of
// deviceInfo + every probe, so we can see exactly where a slow collect spends time.
// Usage: isapi-probe <ip> <user> <pass> [preferBaseURL]
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/coralsearesorts/hims/internal/isapi"
)

func main() {
	if len(os.Args) < 4 {
		fmt.Println("usage: isapi-probe <ip> <user> <pass> [preferBaseURL]")
		os.Exit(2)
	}
	ip, user, pass := os.Args[1], os.Args[2], os.Args[3]
	var prefer []string
	if len(os.Args) > 4 {
		prefer = []string{os.Args[4]}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	t0 := time.Now()
	di, derr := isapi.CollectDeviceInfo(ctx, ip, user, pass, nil, prefer)
	fmt.Printf("deviceInfo: %v in %dms (endpoint=%s err=%v)\n", di.Model+"/"+di.DeviceType, time.Since(t0).Milliseconds(), di.Endpoint, derr)

	t1 := time.Now()
	nvr, err := isapi.Collect(ctx, ip, user, pass, nil, prefer)
	fmt.Printf("Collect: %dms total, err=%v, channels=%d storage=%d\n", time.Since(t1).Milliseconds(), err, len(nvr.Channels), len(nvr.Storage))
	for _, p := range nvr.Probes {
		fmt.Printf("  %6dms  %-3d ok=%-5v  %s\n", p.DurationMs, p.Status, p.OK, p.Path)
	}
	for _, c := range nvr.Channels {
		on := "?"
		if c.Online != nil {
			on = fmt.Sprintf("%v", *c.Online)
		}
		rec := "?"
		if c.Recording != nil {
			rec = fmt.Sprintf("%v", *c.Recording)
		}
		fmt.Printf("  ch%-3d online=%-5s rec=%-5s res=%-10s %s\n", c.No, on, rec, c.Resolution, c.Name)
	}
}

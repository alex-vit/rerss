package main

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/gorilla/feeds"
	"github.com/mmcdole/gofeed"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
)

//go:embed index.html
var indexHTML []byte

func main() {
	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/status", statusHandler)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	ip6, port := os.Getenv("IP"), os.Getenv("PORT")
	addr := fmt.Sprintf("[%s]:%s", ip6, port)

	server := &http.Server{
		BaseContext: func(net.Listener) context.Context { return ctx },
		Addr:        addr,
	}
	server.ListenAndServe()
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if len(query) == 0 {
		w.Write(indexHTML)
		return
	}

	var keepItem func(title string) bool
	if query.Has("re") {
		pattern := query.Get("re")
		regex, err := regexp.Compile(pattern)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		keepItem = regex.MatchString
	} else if skips, specified := query["skip"]; specified {
		keepItem = func(contents string) bool {
			for _, word := range strings.Fields(contents) {
				if slices.Contains(skips, word) {
					return false
				}
			}
			return true
		}
	} else {
		http.Error(w, "missing 'skip' or 're'", http.StatusBadRequest)
		return
	}

	if !query.Has("url") {
		http.Error(w, "missing 'url'", http.StatusBadRequest)
		return
	}
	rssURL := query.Get("url")

	err := writeFilteredRSS(w, keepItem, rssURL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func writeFilteredRSS(w io.Writer, keepItem func(title string) bool, rssURL string) error {
	originalFeed, err := gofeed.NewParser().ParseURL(rssURL)
	if err != nil {
		return err
	}

	filteredFeed := &feeds.Feed{
		Title:       originalFeed.Title,
		Link:        &feeds.Link{Href: originalFeed.Link},
		Description: originalFeed.Description,
		Created:     time.Now(),
	}
	if originalFeed.Author != nil {
		filteredFeed.Author = &feeds.Author{Name: originalFeed.Author.Name, Email: originalFeed.Author.Email}
	}
	for _, item := range originalFeed.Items {
		keep := keepItem(item.Title)
		if keep {
			filteredFeed.Items = append(filteredFeed.Items, &feeds.Item{
				Title:       item.Title,
				Link:        &feeds.Link{Href: item.Link},
				Description: item.Description,
				Author:      &feeds.Author{Name: item.Author.Name, Email: item.Author.Email},
				Created:     *item.PublishedParsed,
			})
		}
	}

	return filteredFeed.WriteRss(w)
}

var statusPattern = strings.TrimSpace(`
CPU used:	%.2f%%
RAM used:	%d / %d / %d MB (%.0f%%)
Goroutines:	%d
`)

func statusHandler(w http.ResponseWriter, r *http.Request) {
	cpuUsages, _ := cpu.Percent(0, false)
	cpuUsage := 0.0
	if len(cpuUsages) > 0 {
		cpuUsage = cpuUsages[0]
	}

	goMem := &runtime.MemStats{}
	runtime.ReadMemStats(goMem)
	sysMem, _ := mem.VirtualMemory()

	numGos := runtime.NumGoroutine()

	fmt.Fprintf(w, statusPattern,
		cpuUsage,
		// memStat.Used/1_024/1_024, memStat.Total/1_024/1_024, memStat.UsedPercent)
		// stats on this host are off by a 1024...
		goMem.Alloc/1_024/1_024, sysMem.Used/1_024/1_024/1_024, sysMem.Total/1_024/1_024/1_024, sysMem.UsedPercent,
		numGos)
}

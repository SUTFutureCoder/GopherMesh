package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"time"
)

func main() {
	port := flag.String("port", "19081", "Listen port")
	name := flag.String("name", "http-hello", "Service name")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"service":   *name,
			"method":    r.Method,
			"path":      r.URL.Path,
			"query":     r.URL.Query(),
			"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		})
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"service": *name,
			"status":  "ok",
		})
	})

	addr := "127.0.0.1:" + *port
	log.Printf("[%s] listening on %s", *name, addr)
	// 每秒钟打印一条虚拟访问日志，包含参数
	go func(service string) {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		paths := []string{"/", "/healthz", "/docs"}
		seq := 1
		for now := range ticker.C {
			payload, err := json.Marshal(map[string]any{
				"type":       "virtual_request",
				"service":    service,
				"request_id": seq,
				"method":     "GET",
				"path":       paths[(seq-1)%len(paths)],
				"query": map[string]any{
					"demo":   "log-panel",
					"seq":    seq,
					"source": "sample/l7http/hello",
				},
				"user_agent": "GopherMeshDocDemo/1.0",
				"timestamp":  now.UTC().Format(time.RFC3339Nano),
			})
			if err != nil {
				log.Printf("[%s] virtual_request marshal failed: %v", service, err)
				continue
			}

			log.Printf("[%s] %s", service, payload)
			seq++
		}
	}(*name)

	log.Fatal(http.ListenAndServe(addr, mux))
}

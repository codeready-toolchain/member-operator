package contentserver

import (
	"bytes"
	"embed"
	"net/http"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"strings"
	"sync"
)

var (
	log = logf.Log.WithName("web_console_content_server")
)

//go:embed static/*
var staticFiles embed.FS

type ContentServer interface {
	HandleContentRequest(w http.ResponseWriter, r *http.Request)
}

type ContentServerConfig interface {
	PendoKey() string
	PendoHost() string
}

type contentServer struct {
	config ContentServerConfig
	rw     sync.RWMutex
	cache  map[string][]byte
}

func NewContentServer(config ContentServerConfig) ContentServer {
	return &contentServer{
		config: config,
		cache:  map[string][]byte{},
	}
}

func (s *contentServer) HandleContentRequest(w http.ResponseWriter, r *http.Request) {
	var path string
	if r.RequestURI == "/status" {
		// Health status check. Use plugin-manifest.json as our health status endpoint but do not log the request to reduce noise in the logs.
		path = "/plugin-manifest.json"
	} else {
		path = r.RequestURI
		log.Info("Requesting...", "URI", path, "Method", r.Method)
	}

	data, err := s.loadResource(path)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		log.Error(err, "error while loading resource", "URI", path, "Method", r.Method)
	}

	var contentType string
	if strings.HasSuffix(path, ".js") {
		contentType = "application/javascript"
	} else if strings.HasSuffix(path, ".json") {
		contentType = "application/json"
	}
	if contentType != "" {
		w.Header().Set("Content-Type", contentType) // Has to be set before calling w.WriteHeader()!
	}

	if _, err := w.Write(data); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Error(err, "unable to write response", "URI", path, "Method", r.Method)
	}

	if r.RequestURI != "/status" {
		log.Info("OK", "URI", path, "Method", r.Method, "Content-Type", w.Header().Get("Content-Type"))
	}
}

func (s *contentServer) loadResource(path string) ([]byte, error) {
	data := s.loadResourceFromCache(path)
	if data != nil {
		return data, nil
	}

	return s.validateCachedResource(path)
}

func (s *contentServer) loadResourceFromCache(path string) []byte {
	s.rw.RLock()
	defer s.rw.RUnlock()

	if val, ok := s.cache[path]; ok {
		return val
	}

	return nil
}

func (s *contentServer) validateCachedResource(path string) ([]byte, error) {
	s.rw.Lock()
	defer s.rw.Unlock()

	fileData, err := staticFiles.ReadFile("static" + path)
	if err != nil {
		return nil, err
	}

	transformed := s.insertPendoKey(fileData, s.config.PendoKey())
	transformed = s.insertPendoHost(transformed, s.config.PendoHost())
	s.cache[path] = transformed

	return transformed, nil
}

func (s *contentServer) insertPendoKey(content []byte, key string) []byte {
	return bytes.Replace(content, []byte("{PENDO_KEY}"), []byte(key), -1)
}

func (s *contentServer) insertPendoHost(content []byte, host string) []byte {
	return bytes.Replace(content, []byte("{PENDO_HOST}"), []byte(host), -1)
}

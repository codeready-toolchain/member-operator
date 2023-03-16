package scriptserver

import (
	"bytes"
	"embed"
	"net/http"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"strings"
	"sync"
)

const (
	pendoTestK = "df54af19-2d86-4f23-7616-81c1822ecaf3"
)

var (
	log = logf.Log.WithName("web_console_script_server")
)

//go:embed static/*
var staticFiles embed.FS

type ScriptServer interface {
	HandleScriptRequest(w http.ResponseWriter, r *http.Request)
}

type scriptServer struct {
	rw    sync.RWMutex
	cache map[string][]byte
}

func NewScriptServer() ScriptServer {
	return &scriptServer{
		cache: map[string][]byte{},
	}
}

func (s *scriptServer) HandleScriptRequest(w http.ResponseWriter, r *http.Request) {
	log.Info("Requesting...", "URI", r.RequestURI, "Method", r.Method)
	data, err := s.loadResource(r.RequestURI)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		log.Error(err, "error while loading resource", "URI", r.RequestURI, "Method", r.Method)
	}

	var contentType string
	if strings.HasSuffix(r.RequestURI, ".js") {
		contentType = "application/javascript"
	} else if strings.HasSuffix(r.RequestURI, ".json") {
		contentType = "application/json"
	}
	if contentType != "" {
		w.Header().Set("Content-Type", contentType) // Has to be set before calling w.WriteHeader()!
	}

	if _, err := w.Write(data); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Error(err, "unable to write response", "URI", r.RequestURI, "Method", r.Method)
	}

	log.Info("OK", "URI", r.RequestURI, "Method", r.Method, "Content-Type", w.Header().Get("Content-Type"))
}

func (s *scriptServer) loadResource(path string) ([]byte, error) {
	data := s.loadResourceFromCache(path)
	if data != nil {
		return data, nil
	}

	return s.validateCachedResource(path)
}

func (s *scriptServer) loadResourceFromCache(path string) []byte {
	s.rw.RLock()
	defer s.rw.RUnlock()

	if val, ok := s.cache[path]; ok {
		return val
	}

	return nil
}

func (s *scriptServer) validateCachedResource(path string) ([]byte, error) {
	s.rw.Lock()
	defer s.rw.Unlock()

	fileData, err := staticFiles.ReadFile("static" + path)
	if err != nil {
		return nil, err
	}

	fileDataWithKey := s.insertPendoKey(fileData, pendoTestK) // TODO load the key from configuration instead
	s.cache[path] = fileDataWithKey

	return fileDataWithKey, nil
}

func (s *scriptServer) insertPendoKey(originalFileContent []byte, key string) []byte {
	return bytes.Replace(originalFileContent, []byte("{INSERT_KEY_HERE}"), []byte(key), -1)
}

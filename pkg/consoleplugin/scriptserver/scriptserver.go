package scriptserver

import (
	"embed"
	"net/http"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sync"
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
	data, err := s.loadResource(r.RequestURI)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		log.Error(err, "error while loading resource")
	}

	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(data); err != nil {
		log.Error(err, "unable to write response")
	}
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

	s.cache[path] = fileData

	return fileData, nil
}

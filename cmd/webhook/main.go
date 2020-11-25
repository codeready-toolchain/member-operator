package main

import (
	"github.com/operator-framework/operator-sdk/pkg/log/zap"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var log = logf.Log.WithName("cmd")

func main() {
	logf.SetLogger(zap.Logger())
	log.Info("Hello world from member operator webhook image")
}

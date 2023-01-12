// Copyright 2023 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"flag"
	"os"
	"time"

	podpercon "github.com/Matir/podperconn"
	"github.com/Matir/podperconn/log"
)

var (
	logger         = log.GetLogger("main")
	deploymentFlag = flag.String("deployment", "", "Path to deployment template for individual pods.")
	logFileFlag    = flag.String("logfile", "", "Log to a file, otherwise stderr will be used.")
	kubeEndpoint   = flag.String("endpoint", "", "Endpoint for API connections, otherwise KUBERNETES_SERVICE_HOST/KUBERNETES_SERVICE_PORT will be used.")
	caPath         = flag.String("ca-path", "", "Path for server (not client) CA")
	keyPath        = flag.String("key-path", "", "Path for client private key")
	certPath       = flag.String("cert-path", "", "Path for client certificate")
	testDeployment = flag.Bool("test-deployment", false, "Perform a test deployment at startup.")
	namespaceFlag  = flag.String("namespace", podpercon.DEFAULT_NAMESPACE, "Kubernetes namespace for deployments.")
	statusMsgFlag  = flag.Bool("status-messages", false, "Include status messages in network stream.  Only suitable for interactive protocols.")
	listenPortFlag = flag.Int("listen-port", 4444, "Port on which to listen for connections")
	logLevel       = flag.String("log-level", "info", "Log level to output.  panic/fatal/error/warn/info/debug/trace")
	deployTimeout  = flag.Duration("deploy-timeout", time.Minute, "Time after which deployment is considered to fail.")
	sessionTimeout = flag.Duration("session-timeout", 60*time.Minute, "Total time for a single session.")
)

func main() {
	flag.Parse()

	if *logFileFlag != "" {
		fp, err := os.Create(*logFileFlag)
		if err != nil {
			logger.WithError(err).Fatal("Failed to open log file!")
			return
		}
		log.SetSink(fp)
		defer fp.Close()
	}

	if *logLevel != "" {
		if err := log.SetLevelFromString(*logLevel); err != nil {
			logger.WithError(err).Fatal("Failed setting log level")
			return
		}
	}

	if *deploymentFlag == "" {
		logger.Error("deployment flag is required")
		usage()
		return
	}

	if *listenPortFlag > 65535 || *listenPortFlag < 1 {
		logger.Error("listen port must be 1-65535")
		usage()
		return
	}
	listenPort := uint16(*listenPortFlag)

	ctrl, err := podpercon.NewContainerController(
		podpercon.WithEndpoint(*kubeEndpoint),
		podpercon.WithCAPath(*caPath),
		podpercon.WithCertPath(*certPath),
		podpercon.WithKeyPath(*keyPath),
		podpercon.WithDeploymentPath(*deploymentFlag),
		podpercon.WithNamespace(*namespaceFlag),
	)
	if err != nil {
		logger.WithError(err).Fatal("Error setting up container controller.")
	}

	if *testDeployment {
		ctx := context.Background()
		uniqid := podpercon.RandID()
		//res, err := ctrl.MakeDeployment(ctx, uniqid)
		res, err := ctrl.MakeDeploymentAndWaitForReady(ctx, uniqid)
		if err != nil {
			logger.WithError(err).Fatal("Error creating test deployment.")
		}
		name := res.ObjectMeta.Name
		logger.WithField("name", name).WithField("uniqid", uniqid).Info("Created deployment")
	}

	listener := podpercon.NewListener(ctrl, listenPort, *deployTimeout, *sessionTimeout)
	if err := listener.ListenAndServe(); err != nil {
		logger.WithError(err).Error("error serving")
	}
}

func usage() {
	flag.PrintDefaults()
	os.Exit(1)
}

// Copyright 2020 Google LLC
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

// This package is the primary infected keys upload service.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/google/exposure-notifications-server/internal/buildinfo"
	"github.com/google/exposure-notifications-server/internal/publish"
	"github.com/google/exposure-notifications-server/internal/setup"
	"github.com/google/exposure-notifications-server/pkg/logging"
	_ "github.com/google/exposure-notifications-server/pkg/observability"
	"github.com/google/exposure-notifications-server/pkg/server"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/sethvargo/go-signalcontext"
)

func main() {
	ctx, done := signalcontext.OnInterrupt()

	logger := logging.NewLoggerFromEnv().
		With("build_id", buildinfo.BuildID).
		With("build_tag", buildinfo.BuildTag)
	ctx = logging.WithLogger(ctx, logger)

	err := realMain(ctx)
	done()

	if err != nil {
		logger.Fatal(err)
	}
}
func realMain(ctx context.Context) error {
	logger := logging.FromContext(ctx)

	var config publish.Config
	env, err := setup.Setup(ctx, &config)
	if err != nil {
		return fmt.Errorf("setup.Setup: %w", err)
	}
	defer env.Close(ctx)

	r := mux.NewRouter()
	r.Handle("/health", server.HandleHealthz(ctx))
	handler, err := publish.NewHandler(ctx, &config, env)
	if err != nil {
		return fmt.Errorf("publish.NewHandler: %w", err)
	}

	// Handle v1 API - this route has to come before the v1alpha route because of
	// path matching.
	r.Handle("/v1/publish", handler.Handle())
	r.Handle("/v1/publish/", http.NotFoundHandler())

	// Handle stats retrieval API
	r.Handle("/v1/stats", handler.HandleStats())
	r.Handle("/v1/stats/", http.NotFoundHandler())

	// Serving of v1alpha1 is on by default, but can be disabled through env var.
	if config.EnableV1Alpha1API {
		r.Handle("/", handler.HandleV1Alpha1())
	}

	srv, err := server.New(config.Port)
	if err != nil {
		return fmt.Errorf("server.New: %w", err)
	}
	logger.Infof("listening on :%s", config.Port)

	return srv.ServeHTTPHandler(ctx, handlers.CombinedLoggingHandler(os.Stdout, r))
}

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

// Package publish defines the exposure keys publishing API.
package publish

import (
	"fmt"
	"net/http"

	"go.opencensus.io/stats"
	"go.opencensus.io/trace"

	"github.com/google/exposure-notifications-server/internal/jsonutil"
	"github.com/google/exposure-notifications-server/internal/maintenance"
	verifyapi "github.com/google/exposure-notifications-server/pkg/api/v1"
	"github.com/google/exposure-notifications-server/pkg/api/v1alpha1"
	"github.com/google/exposure-notifications-server/pkg/logging"
	"github.com/mikehelmick/go-chaff"
)

func (h *PublishHandler) handleV1Apha1Request(w http.ResponseWriter, r *http.Request) *response {
	ctx, span := trace.StartSpan(r.Context(), "(*publish.PublishHandler).handleRequest")
	defer span.End()

	w.Header().Set(HeaderAPIVersion, "v1alpha")

	var data v1alpha1.Publish
	code, err := jsonutil.Unmarshal(w, r, &data)
	if err != nil {
		message := fmt.Sprintf("error unmarshalling API call, code: %v: %v", code, err)
		span.SetStatus(trace.Status{Code: trace.StatusCodeInternal, Message: message})
		return &response{
			status:      code,
			pubResponse: &verifyapi.PublishResponse{ErrorMessage: message}, // will be down-converted in ServeHTTP
			metrics: func() {
				stats.Record(ctx, mBadJSON.M(1))
			},
		}
	}

	// Upconvert the exposure key records.
	v1keys := make([]verifyapi.ExposureKey, len(data.Keys))
	for i, k := range data.Keys {
		v1keys[i] = verifyapi.ExposureKey{
			Key:              k.Key,
			IntervalNumber:   k.IntervalNumber,
			IntervalCount:    k.IntervalCount,
			TransmissionRisk: k.TransmissionRisk,
		}
	}

	// Upconvert v1alpha1 to verifyapi.
	publish := verifyapi.Publish{
		Keys:                 v1keys,
		HealthAuthorityID:    data.AppPackageName,
		VerificationPayload:  data.VerificationPayload,
		HMACKey:              data.HMACKey,
		SymptomOnsetInterval: data.SymptomOnsetInterval,
		Traveler:             len(data.Regions) > 1, // if the key is in more than 1 region, upgrade to traveler status.
		RevisionToken:        data.RevisionToken,
		Padding:              data.Padding,
	}
	bridge := newVersionBridge(data.Regions)

	clientPlatform := platform(r.UserAgent())
	return h.process(ctx, &publish, clientPlatform, bridge)
}

func (h *PublishHandler) HandleV1Alpha1() http.Handler {
	mResponder := maintenance.New(h.config)
	return h.tracker.HandleTrack(chaff.HeaderDetector("X-Chaff"),
		mResponder.Handle(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				response := h.handleV1Apha1Request(w, r)

				ctx := r.Context()

				if padding, err := generatePadding(h.config.ResponsePaddingMinBytes, h.config.ResponsePaddingRange); err != nil {
					stats.Record(ctx, mPaddingFailed.M(1))
					logging.FromContext(ctx).Errorw("failed to padd response", "error", err)
				} else {
					response.pubResponse.Padding = padding
				}

				// Downgrade the v1 response to a v1alpha1 response.
				alpha1Response := &v1alpha1.PublishResponse{
					RevisionToken:     response.pubResponse.RevisionToken,
					InsertedExposures: response.pubResponse.InsertedExposures,
					Error:             response.pubResponse.ErrorMessage,
					Padding:           response.pubResponse.Padding,
					Warnings:          response.pubResponse.Warnings,
				}

				if response.metrics != nil {
					response.metrics()
				}

				jsonutil.MarshalResponse(w, response.status, alpha1Response)
			})))
}

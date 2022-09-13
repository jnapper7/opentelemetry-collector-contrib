// Copyright The OpenTelemetry Authors
// Modifications Copyright © 2021 Cisco Systems, Inc. and its affiliates.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package apiclarityexporter

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"runtime"
	"strings"

	openapiclient "github.com/go-openapi/runtime/client"
	apiclientops "github.com/openclarity/apiclarity/plugins/api/client/client/operations"
	apiclientmodels "github.com/openclarity/apiclarity/plugins/api/client/models"
	uuid "github.com/satori/go.uuid"
	"go.uber.org/zap"

	"go.opentelemetry.io/collector/component"
	otelconfig "go.opentelemetry.io/collector/config"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	semconv "go.opentelemetry.io/otel/semconv/v1.10.0"
)

type exporter struct {
	// Input configuration.
	config   *Config
	logger   *zap.Logger
	settings component.TelemetrySettings
	// Default user-agent header.
	userAgent string
	service   *apiclientops.Client
}

// Process a single span into APIClarity telemetry
func (e *exporter) processOTelSpan(res pcommon.Resource, _ pcommon.InstrumentationScope, span ptrace.Span) (*apiclientmodels.Telemetry, error) {
	res.Attributes().Range(func(k string, v pcommon.Value) bool {
		e.logger.Debug("Checking resource attributes",
			zap.String("key", k),
			zap.String("value", v.AsString()),
		)
		return true
	})

	attrs := span.Attributes()
	if url, ok := attrs.Get(string(semconv.HTTPURLKey)); !ok || len(url.StringVal()) == 0 {
		e.logger.Debug("Skipping span without http.url attribute",
			zap.String("span.name", span.Name()),
			zap.String("span.kind", span.Kind().String()),
		)
		return nil, errors.New("span has no http.url attribute")
	}

	req := &apiclientmodels.Request{
		Common: &apiclientmodels.Common{
			TruncatedBody: false,
			Time:          span.StartTimestamp().AsTime().Unix(),
			Headers:       []*apiclientmodels.Header{},
		},
	}
	resp := &apiclientmodels.Response{
		Common: &apiclientmodels.Common{
			TruncatedBody: false,
			Time:          span.EndTimestamp().AsTime().Unix(),
			Headers:       []*apiclientmodels.Header{},
		},
	}
	actel := &apiclientmodels.Telemetry{
		DestinationAddress: "",
		SourceAddress:      "",
		Request:            req,
		Response:           resp,
	}

	// Fill in missing data where available.
	if method, ok := attrs.Get(string(semconv.HTTPMethodKey)); ok {
		req.Method = method.AsString()
	}
	if target, ok := attrs.Get(string(semconv.HTTPTargetKey)); ok {
		req.Path = target.AsString()
	}
	if host, ok := attrs.Get(string(semconv.HTTPHostKey)); ok {
		req.Host = host.AsString() // host is Host Header. Is this correct?
	}
	if scheme, ok := attrs.Get(string(semconv.HTTPSchemeKey)); ok {
		actel.Scheme = scheme.AsString()
	}
	if statusCode, ok := attrs.Get(string(semconv.HTTPStatusCodeKey)); ok {
		resp.StatusCode = statusCode.AsString()
	}
	if flavor, ok := attrs.Get(string(semconv.HTTPFlavorKey)); ok {
		req.Common.Version = flavor.AsString()
		resp.Common.Version = flavor.AsString()
	}
	if serverName, ok := attrs.Get(string(semconv.NetHostNameKey)); ok {
		actel.DestinationAddress = serverName.AsString()
	} else if clientIP, ok := attrs.Get(string(semconv.NetHostIPKey)); ok {
		actel.DestinationAddress = clientIP.AsString()
	}
	if serverPort, ok := attrs.Get(string(semconv.NetHostPortKey)); ok {
		actel.DestinationAddress = actel.DestinationAddress + ":" + serverPort.AsString()
	}
	if clientName, ok := attrs.Get(string(semconv.NetPeerNameKey)); ok {
		actel.SourceAddress = clientName.AsString()
	} else if clientIP, ok := attrs.Get(string(semconv.NetPeerIPKey)); ok {
		actel.SourceAddress = clientIP.AsString()
	}
	if clientPort, ok := attrs.Get(string(semconv.NetPeerPortKey)); ok {
		actel.SourceAddress = actel.SourceAddress + ":" + clientPort.AsString()
	}

	attrs.Range(func(k string, v pcommon.Value) bool {
		fmt.Printf("Processing attribute \"%s\": \"%s\"\n", k, v.AsString())
		e.logger.Debug("Converting span attributes",
			zap.String("key", k),
			zap.String("value", v.AsString()),
		)
		// Convert header formats
		s := strings.TrimPrefix(k, "http.request.header.")
		if len(s) < len(k) {
			req.Common.Headers = append(req.Common.Headers, &apiclientmodels.Header{
				Key:   strings.ReplaceAll(s, "_", "-"),
				Value: v.AsString(),
			})
			return true
		}
		s = strings.TrimPrefix(k, "http.response.header.")
		if len(s) < len(k) {
			req.Common.Headers = append(req.Common.Headers, &apiclientmodels.Header{
				Key:   strings.ReplaceAll(s, "_", "-"),
				Value: v.AsString(),
			})
			return true
		}
		return true
	})

	// After parsing headers, we could check if the request id is already there...
	actel.RequestID = uuid.NewV4().String()

	return actel, nil
}

// Create new exporter.
func newExporter(cfg otelconfig.Exporter, set component.ExporterCreateSettings) (*exporter, error) {
	oCfg := cfg.(*Config)

	if err := oCfg.Validate(); err != nil {
		return nil, err
	}

	userAgent := fmt.Sprintf("%s/%s (%s/%s)",
		set.BuildInfo.Description, set.BuildInfo.Version, runtime.GOOS, runtime.GOARCH)

	// client construction is deferred to start
	return &exporter{
		config:    oCfg,
		logger:    set.Logger,
		userAgent: userAgent,
		settings:  set.TelemetrySettings,
		service:   nil,
	}, nil
}

// start actually creates the HTTP client. The client construction is deferred till this point as this
// is the only place we get hold of Extensions which are required to construct auth round tripper.
func (e *exporter) start(_ context.Context, host component.Host) error {
	urlInfo, err := url.Parse(e.config.HTTPClientSettings.Endpoint)
	if err != nil {
		return fmt.Errorf("HTTP endpoint must be a valid URL: %w", err)
	}
	client, err := e.config.HTTPClientSettings.ToClientWithHost(host, e.settings)
	if err != nil {
		return fmt.Errorf("cannot create HTTP client: %w", err)
	}
	runtime := openapiclient.NewWithClient(urlInfo.Host, urlInfo.Path, []string{urlInfo.Scheme}, client)
	e.service = apiclientops.New(runtime, nil).(*apiclientops.Client)
	e.logger.Debug("started client for telemetry", zap.String("url", urlInfo.String()))

	return nil
}

// https://pkg.go.dev/go.opentelemetry.io/collector@v0.56.0/consumer#ConsumeTracesFunc
func (e *exporter) pushTraces(ctx context.Context, td ptrace.Traces) error {
	if e.service == nil {
		e.logger.Debug("Processing traces, but client is not initialized")
		fmt.Print("client is not initialized\n")
		return errors.New("cannot process traces: client is not initialized")
	}

	rspans := td.ResourceSpans()
	fmt.Printf("Processing %d resource spans\n", rspans.Len())
	for i := 0; i < rspans.Len(); i++ {
		rspan := rspans.At(i)
		e.logger.Debug("Processing resource span in trace",
			zap.Int("index", i),
		)
		res := rspan.Resource()

		sspans := rspan.ScopeSpans()
		fmt.Printf("Processing %d scope span\n", sspans.Len())
		for j := 0; j < sspans.Len(); j++ {
			sspan := sspans.At(j)
			e.logger.Debug("Processing scope span",
				zap.Int("index", j),
				zap.String("scope.name", sspan.Scope().Name()),
				zap.String("scope.version", sspan.Scope().Version()),
			)
			scope := sspan.Scope()

			spans := sspan.Spans()
			fmt.Printf("Processing %d spans\n", spans.Len())
			for k := 0; k < spans.Len(); k++ {
				span := spans.At(k)
				attrs := span.Attributes()
				if url, ok := attrs.Get("http.url"); !ok || len(url.StringVal()) == 0 {
					fmt.Printf("Skipping span without http.url attribute\n")
					e.logger.Debug("Skipping span without http.url attribute",
						zap.Int("index", j),
						zap.String("span.name", span.Name()),
						zap.String("span.kind", span.Kind().String()),
					)
					continue
				}
				e.logger.Debug("Processing span",
					zap.Int("index", j),
				)

				// TODO: process rest of spans on error, then return errors
				actel, perr := e.processOTelSpan(res, scope, span)
				if perr != nil {
					return consumererror.NewPermanent(perr)
				}

				err := e.export(ctx, actel)
				if err != nil {
					return consumererror.NewPermanent(err)
				}
			}
		}
	}

	return nil
}

func (e *exporter) export(ctx context.Context, actelemetry *apiclientmodels.Telemetry) error {
	e.logger.Debug("Preparing to make APIClarity telemetry request")

	params := apiclientops.NewPostTelemetryParamsWithContext(ctx).WithBody(actelemetry)
	_, err := e.service.PostTelemetry(params)
	if err != nil {
		formattedErr := fmt.Errorf("failed to post telemetry: %w", err)
		e.logger.Error("Failed to post telemetry",
			zap.Error(err),
		)
		return formattedErr
	}

	// All other errors are retryable, so don't wrap them in consumererror.NewPermanent().
	return nil
}

// Original Copyright The OpenTelemetry Authors
// Modifications Copyright © 2022 Cisco Systems, Inc. and its affiliates.
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
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	otelconfig "go.opentelemetry.io/collector/config"
	"go.opentelemetry.io/collector/config/confighttp"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	semconv "go.opentelemetry.io/otel/semconv/v1.10.0"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/common/testutil"
	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/coreinternal/testdata"

	apibackend "github.com/openclarity/apiclarity/backend/pkg/backend"
	apiconfig "github.com/openclarity/apiclarity/backend/pkg/config"
	apidatabase "github.com/openclarity/apiclarity/backend/pkg/database"
	apimodules "github.com/openclarity/apiclarity/backend/pkg/modules"
	apirest "github.com/openclarity/apiclarity/backend/pkg/rest"
	apitraces "github.com/openclarity/apiclarity/backend/pkg/traces"
	apispeculator "github.com/openclarity/speculator/pkg/speculator"
)

const (
	DefaultAttributeHTTPURL string = "https://uop.stage-eu-1/insert"
)

func TestInvalidConfig(t *testing.T) {
	config := &Config{
		HTTPClientSettings: confighttp.HTTPClientSettings{
			Endpoint: "",
		},
	}
	f := NewFactory()
	set := componenttest.NewNopExporterCreateSettings()
	_, err := f.CreateTracesExporter(context.Background(), set, config)
	require.Error(t, err)
}

func TestTraceNoURL(t *testing.T) {
	addr := testutil.GetAvailableLocalAddress(t)
	exp := startTracesExporter(t, fmt.Sprintf("http://%s/api", addr))
	td := testdata.GenerateTracesOneSpan()
	err := exp.ConsumeTraces(context.Background(), td)
	require.NoError(t, err)
}

func TestTraceNoBackend(t *testing.T) {
	addr := testutil.GetAvailableLocalAddress(t)
	exp := startTracesExporter(t, fmt.Sprintf("http://%s/api", addr))
	td := addTraceAttributes(testdata.GenerateTracesOneSpan())
	assert.Error(t, exp.ConsumeTraces(context.Background(), td))
}

func TestTraceInvalidUrl(t *testing.T) {
	exp := startTracesExporter(t, "http:/\\//this_is_an/*/invalid_url")
	td := addTraceAttributes(testdata.GenerateTracesOneSpan())
	assert.Error(t, exp.ConsumeTraces(context.Background(), td))
}

func TestTraceError(t *testing.T) {
	addr := testutil.GetAvailableLocalAddress(t)

	startTracesReceiver(t, addr, consumertest.NewErr(errors.New("my_error")))
	exp := startTracesExporter(t, fmt.Sprintf("http://%s/api", addr))

	td := addTraceAttributes(testdata.GenerateTracesOneSpan())
	assert.Error(t, exp.ConsumeTraces(context.Background(), td))
}

func TestTraceRoundTrip(t *testing.T) {
	addr := testutil.GetAvailableLocalAddress(t)

	tests := []struct {
		name    string
		baseURL string
	}{
		{
			name:    "onlybase",
			baseURL: fmt.Sprintf("http://%s/api", addr),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sink := new(consumertest.TracesSink)
			startTracesReceiver(t, addr, sink)
			exp := startTracesExporter(t, test.baseURL)

			td := addTraceAttributes(testdata.GenerateTracesOneSpan())
			assert.NoError(t, exp.ConsumeTraces(context.Background(), td))
			require.Eventually(t, func() bool {
				return sink.SpanCount() > 0
			}, 1*time.Second, 10*time.Millisecond)
			allTraces := sink.AllTraces()
			require.Len(t, allTraces, 1)
			assert.EqualValues(t, td, allTraces[0])
		})
	}
}

func TestIssue_4221(t *testing.T) {
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { assert.NoError(t, r.Body.Close()) }()
		compressedData, err := ioutil.ReadAll(r.Body)
		require.NoError(t, err)
		gzipReader, err := gzip.NewReader(bytes.NewReader(compressedData))
		require.NoError(t, err)
		data, err := ioutil.ReadAll(gzipReader)
		require.NoError(t, err)
		base64Data := base64.StdEncoding.EncodeToString(data)
		// Verify same base64 encoded string is received.
		assert.Equal(t, "CscBCkkKIAoMc2VydmljZS5uYW1lEhAKDnVvcC5zdGFnZS1ldS0xCiUKGW91dHN5c3RlbXMubW9kdWxlLnZlcnNpb24SCAoGOTAzMzg2EnoKEQoMdW9wX2NhbmFyaWVzEgExEmUKEEMDhT8Ib0+Mhs8Zi2VR34QSCOVRPDJ5XEG5IgA5QE41aASRrxZBQE41aASRrxZKEAoKc3Bhbl9pbmRleBICGANKHwoNY29kZS5mdW5jdGlvbhIOCgxteUZ1bmN0aW9uMzZ6AA==", base64Data)
		unbase64Data, err := base64.StdEncoding.DecodeString(base64Data)
		require.NoError(t, err)
		tr := ptraceotlp.NewRequest()
		require.NoError(t, tr.UnmarshalProto(unbase64Data))
		span := tr.Traces().ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
		assert.Equal(t, "4303853f086f4f8c86cf198b6551df84", span.TraceID().HexString())
		assert.Equal(t, "e5513c32795c41b9", span.SpanID().HexString())
	}))

	exp := startTracesExporter(t, svr.URL)

	md := ptrace.NewTraces()
	rms := md.ResourceSpans().AppendEmpty()
	rms.Resource().Attributes().UpsertString("service.name", "uop.stage-eu-1")
	rms.Resource().Attributes().UpsertString("outsystems.module.version", "903386")
	ils := rms.ScopeSpans().AppendEmpty()
	ils.Scope().SetName("uop_canaries")
	ils.Scope().SetVersion("1")
	span := ils.Spans().AppendEmpty()

	var traceIDBytes [16]byte
	traceIDBytesSlice, err := hex.DecodeString("4303853f086f4f8c86cf198b6551df84")
	require.NoError(t, err)
	copy(traceIDBytes[:], traceIDBytesSlice)
	span.SetTraceID(pcommon.NewTraceID(traceIDBytes))
	assert.Equal(t, "4303853f086f4f8c86cf198b6551df84", span.TraceID().HexString())

	var spanIDBytes [8]byte
	spanIDBytesSlice, err := hex.DecodeString("e5513c32795c41b9")
	require.NoError(t, err)
	copy(spanIDBytes[:], spanIDBytesSlice)
	span.SetSpanID(pcommon.NewSpanID(spanIDBytes))
	assert.Equal(t, "e5513c32795c41b9", span.SpanID().HexString())

	span.SetEndTimestamp(1634684637873000000)
	span.Attributes().UpsertString(string(semconv.HTTPURLKey), DefaultAttributeHTTPURL)
	span.Attributes().UpsertInt("span_index", 3)
	span.Attributes().UpsertString("code.function", "myFunction36")
	span.SetStartTimestamp(1634684637873000000)

	assert.NoError(t, exp.ConsumeTraces(context.Background(), md))
}

func addTraceAttributes(td ptrace.Traces) ptrace.Traces {
	rspans := td.ResourceSpans()
	for i := 0; i < rspans.Len(); i++ {
		sspans := rspans.At(i).ScopeSpans()
		for j := 0; j < sspans.Len(); j++ {
			spans := sspans.At(j).Spans()
			for k := 0; k < spans.Len(); k++ {
				spans.At(k).Attributes().UpsertString(string(semconv.HTTPURLKey), DefaultAttributeHTTPURL)
			}
		}
	}
	return td
}

func startTracesExporter(t *testing.T, baseURL string) component.TracesExporter {
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig().(*Config)
	cfg.HTTPClientSettings.Endpoint = baseURL
	cfg.QueueSettings.Enabled = false
	cfg.RetrySettings.Enabled = false

	var err error
	set := componenttest.NewNopExporterCreateSettings()
	set.Logger, err = zap.NewDevelopment()
	require.NoError(t, err)
	exp, err := factory.CreateTracesExporter(context.Background(), set, cfg)
	require.NoError(t, err)
	startAndCleanup(t, exp)
	return exp
}

func getAvailablePort(t *testing.T) string {
	addr := testutil.GetAvailableLocalAddress(t)
	assert.True(t, strings.Contains(addr, ":"))
	addrParts := strings.Split(addr, ":")
	return addrParts[len(addrParts)-1]
}

func startTracesReceiver(t *testing.T, addr string, next consumer.Traces) {
	assert.True(t, strings.Contains(addr, ":"))
	addrParts := strings.Split(addr, ":")

	addrPort := addrParts[len(addrParts)-1]
	restPort := getAvailablePort(t)
	healthPort := getAvailablePort(t)

	t.Setenv("DATABASE_DRIVER", "LOCAL")             // for storage
	t.Setenv("NO_K8S_MONITOR", "true")               // local deployment
	t.Setenv("DEPLOYMENT_TYPE", "fake")              // for fuzzer config
	t.Setenv("FAKE_DATA", "true")                    // init db with fake data
	t.Setenv("FAKE_TRACES", "false")                 // init db with fake traces
	t.Setenv("HTTP_TRACES_PORT", addrPort)           // for publishing traces
	t.Setenv("BACKEND_REST_PORT", restPort)          // inventory API port
	t.Setenv("HEALTH_CHECK_ADDRESS", ":"+healthPort) // go health checks

	t.Log("starting apiclarity telemetry receiver at: %v", addr)
	// This is backend.Run() but it is not parameterized there to be used
	// anywhere else. :(
	globalCtx, globalCancel := context.WithCancel(context.Background())

	config, err := apiconfig.LoadConfig()
	require.NoError(t, err)
	dbConfig := apibackend.createDatabaseConfig(config)
	dbHandler := apidatabase.Init(dbConfig)
	dbHandler.StartReviewTableCleaner(globalCtx, time.Duration(config.DatabaseCleanerIntervalSec)*time.Second)

	errChan := make(chan struct{}, 100)

	// var clientset kubernetes.Interface
	// var monitor *apik8smonitor.Monitor

	go dbHandler.CreateFakeData()
	speculator := apispeculator.CreateSpeculator(config.SpeculatorConfig)
	module := apimodules.New(globalCtx, dbHandler, nil)
	backend := apibackend.CreateBackend(config, nil, speculator, dbHandler, module)

	restServer, err := apirest.CreateRESTServer(config.BackendRestPort, speculator, dbHandler, module)
	require.NoError(t, err)
	restServer.Start(errChan)

	tracesServer, err := apitraces.CreateHTTPTracesServer(config.HTTPTracesPort, backend.handleHTTPTrace)
	if err != nil {
		t.Fatalf("Failed to create trace server: %v", err)
	}
	tracesServer.Start(errChan)
	t.Log("APIClarity backend is ready")

	t.Cleanup(func() {
		tracesServer.Stop()
		restServer.Stop()
		globalCancel()
	})
}

func startAndCleanup(t *testing.T, cmp component.Component) {
	require.NoError(t, cmp.Start(context.Background(), componenttest.NewNopHost()))
	t.Cleanup(func() {
		require.NoError(t, cmp.Shutdown(context.Background()))
	})
}

func TestErrorResponses(t *testing.T) {
	addr := testutil.GetAvailableLocalAddress(t)
	errMsgPrefix := fmt.Sprintf("error exporting items, request to http://%s/v1/traces responded with HTTP Status Code ", addr)

	tests := []struct {
		name           string
		responseStatus int
		responseBody   *status.Status
		err            error
		isPermErr      bool
		headers        map[string]string
	}{
		{
			name:           "400",
			responseStatus: http.StatusBadRequest,
			responseBody:   status.New(codes.InvalidArgument, "Bad field"),
			isPermErr:      true,
		},
		{
			name:           "404",
			responseStatus: http.StatusNotFound,
			err:            errors.New(errMsgPrefix + "404"),
		},
		{
			name:           "419",
			responseStatus: http.StatusTooManyRequests,
			responseBody:   status.New(codes.InvalidArgument, "Quota exceeded"),
			err: exporterhelper.NewThrottleRetry(
				errors.New(errMsgPrefix+"429, Message=Quota exceeded, Details=[]"),
				time.Duration(0)*time.Second),
		},
		{
			name:           "503",
			responseStatus: http.StatusServiceUnavailable,
			responseBody:   status.New(codes.InvalidArgument, "Server overloaded"),
			err: exporterhelper.NewThrottleRetry(
				errors.New(errMsgPrefix+"503, Message=Server overloaded, Details=[]"),
				time.Duration(0)*time.Second),
		},
		{
			name:           "503-Retry-After",
			responseStatus: http.StatusServiceUnavailable,
			responseBody:   status.New(codes.InvalidArgument, "Server overloaded"),
			headers:        map[string]string{"Retry-After": "30"},
			err: exporterhelper.NewThrottleRetry(
				errors.New(errMsgPrefix+"503, Message=Server overloaded, Details=[]"),
				time.Duration(30)*time.Second),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/v1/traces", func(writer http.ResponseWriter, request *http.Request) {
				for k, v := range test.headers {
					writer.Header().Add(k, v)
				}
				writer.WriteHeader(test.responseStatus)
				if test.responseBody != nil {
					msg, err := proto.Marshal(test.responseBody.Proto())
					require.NoError(t, err)
					_, err = writer.Write(msg)
					require.NoError(t, err)
				}
			})
			srv := http.Server{
				Addr:    addr,
				Handler: mux,
			}
			ln, err := net.Listen("tcp", addr)
			require.NoError(t, err)
			go func() {
				_ = srv.Serve(ln)
			}()

			cfg := &Config{
				ExporterSettings: otelconfig.NewExporterSettings(otelconfig.NewComponentID(typeStr)),
				// Create without QueueSettings and RetrySettings so that ConsumeTraces
				// returns the errors that we want to check immediately.
			}
			set := componenttest.NewNopExporterCreateSettings()
			set.Logger, err = zap.NewDevelopment()
			require.NoError(t, err)
			exp, err := CreateTracesExporter(context.Background(), set, cfg)
			require.NoError(t, err)

			// start the exporter
			err = exp.Start(context.Background(), componenttest.NewNopHost())
			require.NoError(t, err)
			t.Cleanup(func() {
				require.NoError(t, exp.Shutdown(context.Background()))
			})

			// generate traces
			traces := ptrace.NewTraces()
			err = exp.ConsumeTraces(context.Background(), traces)
			assert.Error(t, err)

			if test.isPermErr {
				assert.True(t, consumererror.IsPermanent(err))
			} else {
				assert.EqualValues(t, test.err, err)
			}

			srv.Close()
		})
	}
}

func TestUserAgent(t *testing.T) {
	var err error
	addr := testutil.GetAvailableLocalAddress(t)
	set := componenttest.NewNopExporterCreateSettings()
	set.BuildInfo.Description = "Collector"
	set.BuildInfo.Version = "1.2.3test"
	set.Logger, err = zap.NewDevelopment()
	require.NoError(t, err)

	tests := []struct {
		name       string
		headers    map[string]string
		expectedUA string
	}{
		{
			name:       "default_user_agent",
			expectedUA: "Collector/1.2.3test",
		},
		{
			name:       "custom_user_agent",
			headers:    map[string]string{"User-Agent": "My Custom Agent"},
			expectedUA: "My Custom Agent",
		},
		{
			name:       "custom_user_agent_lowercase",
			headers:    map[string]string{"user-agent": "My Custom Agent"},
			expectedUA: "My Custom Agent",
		},
	}

	t.Run("traces", func(t *testing.T) {
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				mux := http.NewServeMux()
				mux.HandleFunc("/v1/traces", func(writer http.ResponseWriter, request *http.Request) {
					assert.Contains(t, request.Header.Get("user-agent"), test.expectedUA)
					writer.WriteHeader(200)
				})
				srv := http.Server{
					Addr:    addr,
					Handler: mux,
				}
				ln, err := net.Listen("tcp", addr)
				require.NoError(t, err)
				go func() {
					_ = srv.Serve(ln)
				}()

				factory := NewFactory()
				cfg := factory.CreateDefaultConfig().(*Config)
				cfg.HTTPClientSettings = confighttp.HTTPClientSettings{
					Headers:  test.headers,
					Endpoint: fmt.Sprintf("http://%s/api", addr),
				}

				exp, err := CreateTracesExporter(context.Background(), set, cfg)
				require.NoError(t, err)

				// start the exporter
				err = exp.Start(context.Background(), componenttest.NewNopHost())
				require.NoError(t, err)
				t.Cleanup(func() {
					require.NoError(t, exp.Shutdown(context.Background()))
				})

				// generate data
				traces := ptrace.NewTraces()
				err = exp.ConsumeTraces(context.Background(), traces)
				require.NoError(t, err)

				srv.Close()
			})
		}
	})
}

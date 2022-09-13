// Copyright The OpenTelemetry Authors
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
	"time"

	"go.opentelemetry.io/collector/component"
	otelconfig "go.opentelemetry.io/collector/config"
	"go.opentelemetry.io/collector/config/confighttp"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/exporter/exporterhelper"
	"go.uber.org/zap"
)

const (
	// The value of "type" key in configuration.
	typeStr = "apiclarity"
)

// NewFactory creates a factory for OTLP exporter.
func NewFactory() component.ExporterFactory {
	return component.NewExporterFactory(
		typeStr,
		CreateDefaultConfig,
		component.WithTracesExporter(CreateTracesExporter))
}

func CreateDefaultConfig() otelconfig.Exporter {
	return &Config{
		ExporterSettings: otelconfig.NewExporterSettings(otelconfig.NewComponentID(typeStr)),
		RetrySettings:    exporterhelper.NewDefaultRetrySettings(),
		QueueSettings:    exporterhelper.NewDefaultQueueSettings(),
		HTTPClientSettings: confighttp.HTTPClientSettings{
			Endpoint: "",
			Timeout:  30 * time.Second,
			Headers:  map[string]string{},
			// We almost read 0 bytes, so no need to tune ReadBufferSize.
			WriteBufferSize: 512 * 1024,
		},
	}
}

func CreateTracesExporter(
	_ context.Context,
	set component.ExporterCreateSettings,
	cfg otelconfig.Exporter,
) (component.TracesExporter, error) {
	oce, err := newExporter(cfg, set)
	if err != nil {
		set.Logger.Error("Failed to create new exporter", zap.Error(err))
		return nil, err
	}
	oCfg := cfg.(*Config)

	if oCfg.HTTPClientSettings.Endpoint == "" {
		err = errors.New("endpoint must be specified")
		set.Logger.Error("configuration error", zap.Error(err))
		return nil, err
	}

	return exporterhelper.NewTracesExporter(
		cfg,
		set,
		oce.pushTraces,
		exporterhelper.WithStart(oce.start),
		exporterhelper.WithCapabilities(consumer.Capabilities{MutatesData: false}),
		// explicitly disable since we rely on http.Client timeout logic.
		exporterhelper.WithTimeout(exporterhelper.TimeoutSettings{Timeout: 0}),
		exporterhelper.WithRetry(oCfg.RetrySettings),
		exporterhelper.WithQueue(oCfg.QueueSettings))
}

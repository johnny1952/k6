/*
 *
 * k6 - a next-generation load testing tool
 * Copyright (C) 2016 Load Impact
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as
 * published by the Free Software Foundation, either version 3 of the
 * License, or (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 *
 */

package core

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/loadimpact/k6/core/local"
	"github.com/loadimpact/k6/js"
	"github.com/loadimpact/k6/lib"
	"github.com/loadimpact/k6/lib/types"
	"github.com/loadimpact/k6/stats"
	"github.com/loadimpact/k6/stats/dummy"
	"github.com/mccutchen/go-httpbin/httpbin"
	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/guregu/null.v3"
)

type testErrorWithString string

func (e testErrorWithString) Error() string  { return string(e) }
func (e testErrorWithString) String() string { return string(e) }

// Apply a null logger to the engine and return the hook.
func applyNullLogger(e *Engine) *logtest.Hook {
	logger, hook := logtest.NewNullLogger()
	e.SetLogger(logger)
	return hook
}

// Wrapper around newEngine that applies a null logger.
func newTestEngine(ex lib.Executor, opts lib.Options) (*Engine, error, *logtest.Hook) {
	e, err := NewEngine(ex, opts)
	if err != nil {
		return e, err, nil
	}
	hook := applyNullLogger(e)
	return e, nil, hook
}

func L(r lib.Runner) lib.Executor {
	return local.New(r)
}

func LF(fn func(ctx context.Context) ([]stats.Sample, error)) lib.Executor {
	return L(&lib.MiniRunner{Fn: fn})
}

func TestNewEngine(t *testing.T) {
	_, err, _ := newTestEngine(nil, lib.Options{})
	assert.NoError(t, err)
}

func TestNewEngineOptions(t *testing.T) {
	t.Run("Duration", func(t *testing.T) {
		e, err, _ := newTestEngine(nil, lib.Options{
			Duration: types.NullDurationFrom(10 * time.Second),
		})
		assert.NoError(t, err)
		assert.Nil(t, e.Executor.GetStages())
		assert.Equal(t, types.NullDurationFrom(10*time.Second), e.Executor.GetEndTime())

		t.Run("Infinite", func(t *testing.T) {
			e, err, _ := newTestEngine(nil, lib.Options{Duration: types.NullDuration{}})
			assert.NoError(t, err)
			assert.Nil(t, e.Executor.GetStages())
			assert.Equal(t, types.NullDuration{}, e.Executor.GetEndTime())
		})
	})
	t.Run("Stages", func(t *testing.T) {
		e, err, _ := newTestEngine(nil, lib.Options{
			Stages: []lib.Stage{
				{Duration: types.NullDurationFrom(10 * time.Second), Target: null.IntFrom(10)},
			},
		})
		assert.NoError(t, err)
		if assert.Len(t, e.Executor.GetStages(), 1) {
			assert.Equal(t, e.Executor.GetStages()[0], lib.Stage{Duration: types.NullDurationFrom(10 * time.Second), Target: null.IntFrom(10)})
		}
	})
	t.Run("Stages/Duration", func(t *testing.T) {
		e, err, _ := newTestEngine(nil, lib.Options{
			Duration: types.NullDurationFrom(60 * time.Second),
			Stages: []lib.Stage{
				{Duration: types.NullDurationFrom(10 * time.Second), Target: null.IntFrom(10)},
			},
		})
		assert.NoError(t, err)
		if assert.Len(t, e.Executor.GetStages(), 1) {
			assert.Equal(t, e.Executor.GetStages()[0], lib.Stage{Duration: types.NullDurationFrom(10 * time.Second), Target: null.IntFrom(10)})
		}
		assert.Equal(t, types.NullDurationFrom(60*time.Second), e.Executor.GetEndTime())
	})
	t.Run("Iterations", func(t *testing.T) {
		e, err, _ := newTestEngine(nil, lib.Options{Iterations: null.IntFrom(100)})
		assert.NoError(t, err)
		assert.Equal(t, null.IntFrom(100), e.Executor.GetEndIterations())
	})
	t.Run("VUsMax", func(t *testing.T) {
		t.Run("not set", func(t *testing.T) {
			e, err, _ := newTestEngine(nil, lib.Options{})
			assert.NoError(t, err)
			assert.Equal(t, int64(0), e.Executor.GetVUsMax())
			assert.Equal(t, int64(0), e.Executor.GetVUs())
		})
		t.Run("set", func(t *testing.T) {
			e, err, _ := newTestEngine(nil, lib.Options{
				VUsMax: null.IntFrom(10),
			})
			assert.NoError(t, err)
			assert.Equal(t, int64(10), e.Executor.GetVUsMax())
			assert.Equal(t, int64(0), e.Executor.GetVUs())
		})
	})
	t.Run("VUs", func(t *testing.T) {
		t.Run("no max", func(t *testing.T) {
			_, err, _ := newTestEngine(nil, lib.Options{
				VUs: null.IntFrom(10),
			})
			assert.EqualError(t, err, "can't raise vu count (to 10) above vu cap (0)")
		})
		t.Run("negative max", func(t *testing.T) {
			_, err, _ := newTestEngine(nil, lib.Options{
				VUsMax: null.IntFrom(-1),
			})
			assert.EqualError(t, err, "vu cap can't be negative")
		})
		t.Run("max too low", func(t *testing.T) {
			_, err, _ := newTestEngine(nil, lib.Options{
				VUsMax: null.IntFrom(1),
				VUs:    null.IntFrom(10),
			})
			assert.EqualError(t, err, "can't raise vu count (to 10) above vu cap (1)")
		})
		t.Run("max higher", func(t *testing.T) {
			e, err, _ := newTestEngine(nil, lib.Options{
				VUsMax: null.IntFrom(10),
				VUs:    null.IntFrom(1),
			})
			assert.NoError(t, err)
			assert.Equal(t, int64(10), e.Executor.GetVUsMax())
			assert.Equal(t, int64(1), e.Executor.GetVUs())
		})
		t.Run("max just right", func(t *testing.T) {
			e, err, _ := newTestEngine(nil, lib.Options{
				VUsMax: null.IntFrom(10),
				VUs:    null.IntFrom(10),
			})
			assert.NoError(t, err)
			assert.Equal(t, int64(10), e.Executor.GetVUsMax())
			assert.Equal(t, int64(10), e.Executor.GetVUs())
		})
	})
	t.Run("Paused", func(t *testing.T) {
		t.Run("not set", func(t *testing.T) {
			e, err, _ := newTestEngine(nil, lib.Options{})
			assert.NoError(t, err)
			assert.False(t, e.Executor.IsPaused())
		})
		t.Run("false", func(t *testing.T) {
			e, err, _ := newTestEngine(nil, lib.Options{
				Paused: null.BoolFrom(false),
			})
			assert.NoError(t, err)
			assert.False(t, e.Executor.IsPaused())
		})
		t.Run("true", func(t *testing.T) {
			e, err, _ := newTestEngine(nil, lib.Options{
				Paused: null.BoolFrom(true),
			})
			assert.NoError(t, err)
			assert.True(t, e.Executor.IsPaused())
		})
	})
	t.Run("thresholds", func(t *testing.T) {
		e, err, _ := newTestEngine(nil, lib.Options{
			Thresholds: map[string]stats.Thresholds{
				"my_metric": {},
			},
		})
		assert.NoError(t, err)
		assert.Contains(t, e.thresholds, "my_metric")

		t.Run("submetrics", func(t *testing.T) {
			e, err, _ := newTestEngine(nil, lib.Options{
				Thresholds: map[string]stats.Thresholds{
					"my_metric{tag:value}": {},
				},
			})
			assert.NoError(t, err)
			assert.Contains(t, e.thresholds, "my_metric{tag:value}")
			assert.Contains(t, e.submetrics, "my_metric")
		})
	})
}

func TestEngineRun(t *testing.T) {
	log.SetLevel(log.DebugLevel)
	t.Run("exits with context", func(t *testing.T) {
		duration := 100 * time.Millisecond
		e, err, _ := newTestEngine(nil, lib.Options{})
		assert.NoError(t, err)

		ctx, cancel := context.WithTimeout(context.Background(), duration)
		defer cancel()
		startTime := time.Now()
		assert.NoError(t, e.Run(ctx))
		assert.WithinDuration(t, startTime.Add(duration), time.Now(), 100*time.Millisecond)
	})
	t.Run("exits with executor", func(t *testing.T) {
		e, err, _ := newTestEngine(nil, lib.Options{
			VUs:        null.IntFrom(10),
			VUsMax:     null.IntFrom(10),
			Iterations: null.IntFrom(100),
		})
		assert.NoError(t, err)
		assert.NoError(t, e.Run(context.Background()))
		assert.Equal(t, int64(100), e.Executor.GetIterations())
	})

	// Make sure samples are discarded after context close (using "cutoff" timestamp in local.go)
	t.Run("collects samples", func(t *testing.T) {
		testMetric := stats.New("test_metric", stats.Trend)

		signalChan := make(chan interface{})
		var e *Engine
		e, err, _ := newTestEngine(LF(func(ctx context.Context) (samples []stats.Sample, err error) {
			samples = append(samples, stats.Sample{Metric: testMetric, Time: time.Now(), Value: 1})
			close(signalChan)
			<-ctx.Done()

			// HACK(robin): Add a sleep here to temporarily workaround two problems with this test:
			// 1. The sample times are compared against the `cutoff` in core/local/local.go and sometimes the
			//    second sample (below) gets a `Time` smaller than `cutoff` because the lines below get executed
			//    before the `<-ctx.Done()` select in local.go:Run() on multi-core systems where
			//    goroutines can run in parallel.
			// 2. Sometimes the `case samples := <-vuOut` gets selected before the `<-ctx.Done()` in
			//    core/local/local.go:Run() causing all samples from this mocked "RunOnce()" function to be accepted.
			time.Sleep(time.Millisecond * 10)
			samples = append(samples, stats.Sample{Metric: testMetric, Time: time.Now(), Value: 2})
			return samples, err
		}), lib.Options{
			VUs:        null.IntFrom(1),
			VUsMax:     null.IntFrom(1),
			Iterations: null.IntFrom(1),
		})
		if !assert.NoError(t, err) {
			return
		}

		c := &dummy.Collector{}
		e.Collector = c

		ctx, cancel := context.WithCancel(context.Background())
		errC := make(chan error)
		go func() { errC <- e.Run(ctx) }()
		<-signalChan
		cancel()
		assert.NoError(t, <-errC)

		found := 0
		for _, s := range c.Samples {
			if s.Metric != testMetric {
				continue
			}
			found++
			assert.Equal(t, 1.0, s.Value, "wrong value")
		}
		assert.Equal(t, 1, found, "wrong number of samples")
	})
}

func TestEngineAtTime(t *testing.T) {
	e, err, _ := newTestEngine(nil, lib.Options{})
	assert.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	assert.NoError(t, e.Run(ctx))
}

func TestEngineCollector(t *testing.T) {
	testMetric := stats.New("test_metric", stats.Trend)

	e, err, _ := newTestEngine(LF(func(ctx context.Context) ([]stats.Sample, error) {
		return []stats.Sample{{Metric: testMetric}}, nil
	}), lib.Options{VUs: null.IntFrom(1), VUsMax: null.IntFrom(1), Iterations: null.IntFrom(1)})
	assert.NoError(t, err)

	c := &dummy.Collector{}
	e.Collector = c

	assert.NoError(t, e.Run(context.Background()))

	cSamples := []stats.Sample{}
	for _, sample := range c.Samples {
		if sample.Metric == testMetric {
			cSamples = append(cSamples, sample)
		}
	}
	metric := e.Metrics["test_metric"]
	if assert.NotNil(t, metric) {
		sink := metric.Sink.(*stats.TrendSink)
		if assert.NotNil(t, sink) {
			numCollectorSamples := len(cSamples)
			numEngineSamples := len(sink.Values)
			assert.Equal(t, numEngineSamples, numCollectorSamples)
		}
	}
}

func TestEngine_processSamples(t *testing.T) {
	metric := stats.New("my_metric", stats.Gauge)

	t.Run("metric", func(t *testing.T) {
		e, err, _ := newTestEngine(nil, lib.Options{})
		assert.NoError(t, err)

		e.processSamples(
			stats.Sample{Metric: metric, Value: 1.25, Tags: map[string]string{"a": "1"}},
		)

		assert.IsType(t, &stats.GaugeSink{}, e.Metrics["my_metric"].Sink)
	})
	t.Run("submetric", func(t *testing.T) {
		ths, err := stats.NewThresholds([]string{`1+1==2`})
		assert.NoError(t, err)

		e, err, _ := newTestEngine(nil, lib.Options{
			Thresholds: map[string]stats.Thresholds{
				"my_metric{a:1}": ths,
			},
		})
		assert.NoError(t, err)

		sms := e.submetrics["my_metric"]
		assert.Len(t, sms, 1)
		assert.Equal(t, "my_metric{a:1}", sms[0].Name)
		assert.EqualValues(t, map[string]string{"a": "1"}, sms[0].Tags)

		e.processSamples(
			stats.Sample{Metric: metric, Value: 1.25, Tags: map[string]string{"a": "1"}},
		)

		assert.IsType(t, &stats.GaugeSink{}, e.Metrics["my_metric"].Sink)
		assert.IsType(t, &stats.GaugeSink{}, e.Metrics["my_metric{a:1}"].Sink)
	})
	t.Run("apply run tags", func(t *testing.T) {
		tags := map[string]string{"foo": "bar"}
		e, err, _ := newTestEngine(nil, lib.Options{RunTags: tags})
		assert.NoError(t, err)

		c := &dummy.Collector{}
		e.Collector = c

		t.Run("sample untagged", func(t *testing.T) {
			c.Samples = nil

			e.processSamples(
				stats.Sample{
					Metric: metric,
					Value:  1.25,
				},
			)

			assert.Equal(t, tags, c.Samples[0].Tags)
		})
		t.Run("sample tagged", func(t *testing.T) {
			c.Samples = nil

			e.processSamples(
				stats.Sample{
					Metric: metric,
					Value:  1.25,
					Tags:   map[string]string{"myTag": "foobar"},
				},
			)

			assert.Equal(t, tags["foo"], c.Samples[0].Tags["foo"])
		})

		e.processSamples(
			stats.Sample{Metric: metric, Value: 1.25, Tags: nil},
		)

	})
}

func TestEngine_runThresholds(t *testing.T) {
	metric := stats.New("my_metric", stats.Gauge)
	thresholds := make(map[string]stats.Thresholds, 1)

	ths, err := stats.NewThresholds([]string{"1+1==3"})
	assert.NoError(t, err)

	t.Run("aborted", func(t *testing.T) {
		ths.Thresholds[0].AbortOnFail = true
		thresholds[metric.Name] = ths
		e, err, _ := newTestEngine(nil, lib.Options{Thresholds: thresholds})
		assert.NoError(t, err)

		e.processSamples(
			stats.Sample{Metric: metric, Value: 1.25, Tags: map[string]string{"a": "1"}},
		)

		ctx, cancel := context.WithCancel(context.Background())
		aborted := false

		cancelFunc := func() {
			cancel()
			aborted = true
		}

		e.runThresholds(ctx, cancelFunc)

		assert.True(t, aborted)
	})

	t.Run("canceled", func(t *testing.T) {
		ths.Abort = false
		thresholds[metric.Name] = ths
		e, err, _ := newTestEngine(nil, lib.Options{Thresholds: thresholds})
		assert.NoError(t, err)

		e.processSamples(
			stats.Sample{Metric: metric, Value: 1.25, Tags: map[string]string{"a": "1"}},
		)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		done := make(chan struct{})
		go func() {
			defer close(done)
			e.runThresholds(ctx, cancel)
		}()

		select {
		case <-done:
			return
		case <-time.After(1 * time.Second):
			assert.Fail(t, "Test should have completed within a second")
		}
	})
}

func TestEngine_processThresholds(t *testing.T) {
	metric := stats.New("my_metric", stats.Gauge)

	testdata := map[string]struct {
		pass  bool
		ths   map[string][]string
		abort bool
	}{
		"passing":  {true, map[string][]string{"my_metric": {"1+1==2"}}, false},
		"failing":  {false, map[string][]string{"my_metric": {"1+1==3"}}, false},
		"aborting": {false, map[string][]string{"my_metric": {"1+1==3"}}, true},

		"submetric,match,passing":   {true, map[string][]string{"my_metric{a:1}": {"1+1==2"}}, false},
		"submetric,match,failing":   {false, map[string][]string{"my_metric{a:1}": {"1+1==3"}}, false},
		"submetric,nomatch,passing": {true, map[string][]string{"my_metric{a:2}": {"1+1==2"}}, false},
		"submetric,nomatch,failing": {true, map[string][]string{"my_metric{a:2}": {"1+1==3"}}, false},
	}

	for name, data := range testdata {
		t.Run(name, func(t *testing.T) {
			thresholds := make(map[string]stats.Thresholds, len(data.ths))
			for m, srcs := range data.ths {
				ths, err := stats.NewThresholds(srcs)
				assert.NoError(t, err)
				ths.Thresholds[0].AbortOnFail = data.abort
				thresholds[m] = ths
			}

			e, err, _ := newTestEngine(nil, lib.Options{Thresholds: thresholds})
			assert.NoError(t, err)

			e.processSamples(
				stats.Sample{Metric: metric, Value: 1.25, Tags: map[string]string{"a": "1"}},
			)

			abortCalled := false

			abortFunc := func() {
				abortCalled = true
			}

			e.processThresholds(abortFunc)

			assert.Equal(t, data.pass, !e.IsTainted())
			if data.abort {
				assert.True(t, abortCalled)
			}
		})
	}
}

func getMetricSum(samples []stats.Sample, name string) (result float64) {
	for _, s := range samples {
		if s.Metric.Name == name {
			result += s.Value
		}
	}
	return
}
func TestSentReceivedMetrics(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(httpbin.NewHTTPBin().Handler())
	defer srv.Close()

	burl := func(bytecount uint32) string {
		return fmt.Sprintf(`"%s/bytes/%d"`, srv.URL, bytecount)
	}

	expectedSingleData := 50000.0

	type testCase struct{ Iterations, VUs int64 }
	testCases := []testCase{
		{1, 1}, {1, 2}, {2, 1}, {2, 2}, {3, 1}, {5, 2}, {10, 3}, {25, 2}, {50, 5},
	}

	getTestCase := func(t *testing.T, tc testCase) func(t *testing.T) {
		return func(t *testing.T) {
			//TODO: figure out why it fails if we uncomment this:
			t.Parallel()
			r, err := js.New(&lib.SourceData{
				Filename: "/script.js",
				Data: []byte(`
					import http from "k6/http";
					export default function() {
						http.get(` + burl(10000) + `);
						http.batch([` + burl(10000) + `,` + burl(20000) + `,` + burl(10000) + `]);
					}
				`),
			}, afero.NewMemMapFs(), lib.RuntimeOptions{})
			require.NoError(t, err)

			options := lib.Options{
				Iterations: null.IntFrom(tc.Iterations),
				VUs:        null.IntFrom(tc.VUs),
				VUsMax:     null.IntFrom(tc.VUs),
			}
			//TODO: test for differences with NoConnectionReuse enabled and disabled

			engine, err := NewEngine(local.New(r), options)
			require.NoError(t, err)

			collector := &dummy.Collector{}
			engine.Collector = collector

			ctx, cancel := context.WithCancel(context.Background())
			errC := make(chan error)
			go func() { errC <- engine.Run(ctx) }()

			select {
			case <-time.After(5 * time.Second):
				cancel()
				t.Fatal("Test timed out")
			case err := <-errC:
				cancel()
				require.NoError(t, err)
			}

			receivedData := getMetricSum(collector.Samples, "data_received")
			expectedDataMin := expectedSingleData * float64(tc.Iterations)
			expectedDataMax := 1.05 * expectedDataMin // To account for headers
			if receivedData < expectedDataMin || receivedData > expectedDataMax {
				t.Errorf(
					"The received data should be in the interval [%f, %f] but was %f",
					expectedDataMin,
					expectedDataMax,
					receivedData,
				)
			}
		}
	}

	// This Run will not return until the parallel subtests complete.
	t.Run("group", func(t *testing.T) {
		for testn, tc := range testCases {
			t.Run(
				fmt.Sprintf("SentReceivedMetrics_%d(%d, %d)", testn, tc.Iterations, tc.VUs),
				getTestCase(t, tc),
			)
		}
	})
}

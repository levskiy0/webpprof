// Package kafka profiles kafka-go producers and consumers.
package kafka

import (
	"context"
	"strconv"
	"time"

	"github.com/levskiy0/webpprof"
	"github.com/segmentio/kafka-go"
)

// Config controls Kafka metadata and optional bounded message-value capture.
type Config struct {
	Connection   string
	Topic        string
	GroupID      string
	CaptureValue bool
	ValueLimit   int
}

// Writer is the kafka-go producer method wrapped by ProfileWriter.
type Writer interface {
	// WriteMessages writes one or more messages using ctx.
	WriteMessages(context.Context, ...kafka.Message) error
}

// Reader is the kafka-go consumer method wrapped by ProfileReader.
type Reader interface {
	// ReadMessage waits for and returns the next message using ctx.
	ReadMessage(context.Context) (kafka.Message, error)
}

// ProfileWriter wraps writer with the default profiler and records each
// producer attempt as a job dispatch.
func ProfileWriter(writer Writer, configs ...Config) Writer {
	return ProfileWriterWith(webpprof.Default(), writer, configs...)
}

// ProfileWriterWith wraps writer with p. Nil profilers or writers are returned
// unchanged.
func ProfileWriterWith(p *webpprof.Profiler, writer Writer, configs ...Config) Writer {
	if p == nil || writer == nil {
		return writer
	}
	return &profiledWriter{inner: writer, profiler: p, config: firstConfig(configs)}
}

// ProfileReader wraps reader with the default profiler and records each
// consumed message as a job.
func ProfileReader(reader Reader, configs ...Config) Reader {
	return ProfileReaderWith(webpprof.Default(), reader, configs...)
}

// ProfileReaderWith wraps reader with p. Nil profilers or readers are returned
// unchanged.
func ProfileReaderWith(p *webpprof.Profiler, reader Reader, configs ...Config) Reader {
	if p == nil || reader == nil {
		return reader
	}
	return &profiledReader{inner: reader, profiler: p, config: firstConfig(configs)}
}

type profiledWriter struct {
	inner    Writer
	profiler *webpprof.Profiler
	config   Config
}

func (w *profiledWriter) WriteMessages(ctx context.Context, messages ...kafka.Message) error {
	startedAt := time.Now().UTC()
	callsite := w.profiler.CaptureCallsite(webpprof.KindJob)
	err := w.inner.WriteMessages(ctx, messages...)
	for _, message := range messages {
		w.profiler.LogJobContext(ctx, messageJob(message, w.config, "dispatched", startedAt, callsite, err))
	}
	return err
}

type profiledReader struct {
	inner    Reader
	profiler *webpprof.Profiler
	config   Config
}

func (r *profiledReader) ReadMessage(ctx context.Context) (kafka.Message, error) {
	startedAt := time.Now().UTC()
	message, err := r.inner.ReadMessage(ctx)
	state := "received"
	if err != nil {
		state = "failed"
	}
	r.profiler.LogJobContext(ctx, messageJob(message, r.config, state, startedAt, nil, err))
	return message, err
}

func messageJob(message kafka.Message, config Config, state string, startedAt time.Time, callsite []webpprof.SourceFrame, err error) webpprof.Job {
	topic := message.Topic
	if topic == "" {
		topic = config.Topic
	}
	tags := map[string]string{
		"messaging.system":    "kafka",
		"messaging.topic":     topic,
		"messaging.partition": strconv.Itoa(message.Partition),
		"messaging.offset":    strconv.FormatInt(message.Offset, 10),
	}
	if config.GroupID != "" {
		tags["messaging.consumer_group"] = config.GroupID
	}
	return webpprof.Job{
		Meta:       webpprof.Meta{StartedAt: startedAt, Duration: time.Since(startedAt), Tags: tags},
		Name:       topic,
		Queue:      topic,
		Connection: config.Connection,
		State:      state,
		Arguments:  messageArguments(message, config),
		Callsite:   callsite,
		Error:      errorString(err),
	}
}

func messageArguments(message kafka.Message, config Config) []webpprof.Argument {
	arguments := make([]webpprof.Argument, 0, 2)
	if len(message.Key) > 0 {
		arguments = append(arguments, webpprof.Argument{Name: "key", Type: "bytes", Size: int64(len(message.Key))})
	}
	if len(message.Value) == 0 {
		return arguments
	}
	argument := webpprof.Argument{Name: "value", Type: "bytes", Size: int64(len(message.Value))}
	if config.CaptureValue {
		limit := config.ValueLimit
		if limit <= 0 {
			limit = 4096
		}
		value := message.Value
		if len(value) > limit {
			value = value[:limit]
			argument.Truncated = true
		}
		argument.Value = string(value)
	}
	return append(arguments, argument)
}

func firstConfig(configs []Config) Config {
	if len(configs) == 0 {
		return Config{}
	}
	return configs[0]
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

var _ Writer = (*profiledWriter)(nil)
var _ Reader = (*profiledReader)(nil)

# EventSource

Server-Sent Events client in Go. The library is in development and is not tested much. I'm a bit lazy with it. Will it ever reach v1.0? Who knows...

# Usage

```go
package main

import (
	"github.com/nsf/eventsource"
	"log/slog"
	"os"
	"os/signal"
)

func handleMessage(message eventsource.Message, err error) {
	if err != nil {
		slog.Error("error", "err", err)
		return
	}
	// This function is called in EventSource processing goroutine. In real world usage scenario you want to avoid
	// stalling it. Copy everything and defer processing of the message contents. It might be okay to do some
	// minor processing, such as discarding irrelevant event types.
	id := string(message.ID)
	event := string(message.Event)
	data := string(message.Data)
	go func() {
		slog.Info("message", "id", id, "event", event, "data", data)
	}()
}

func main() {
	es, err := eventsource.New(
		eventsource.WithURL("https://sse.dev/test"),
		eventsource.WithCallback(handleMessage),
	)
	if err != nil {
		panic(err)
	}
	defer es.Close()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
}
```
# EventSource

Server-Sent Events client in Go. The library is in development and is not tested much. I'm a bit lazy with it. Will it ever reach v1.0? Who knows...

# Features

I tried to make this library minimalistic and fast.

- API is dead simple: create `*EventSource` with `New()` function and then `Close()` it when you no longer need it. `New()` function uses widely understood Options Pattern to provide flexible configuration.
- Library uses a minimal amount of sync primitives. Internally it starts a goroutine which is later terminated via context (`context.WithCancel()`). You can cancel the parent context yourself or call `Close()` function. In addition to cancelling internal context, `Close()` function waits on `sync.WaitGroup` for internal goroutine to return, this guarantees that callback is not invoked after `Close()` is returned.
- Library carefully manages memory. Effectively it does minor buffering and processing of the `*http.Response`. You can control max sizes of all the buffers involved. Buffers are reused within a single `*http.Response` connection. Callback is invoked with the `Message` structure which contains fields pointing at internal buffers (no extra copies are done). Callback call is performed synchronously from the internal goroutine. You're encouraged to copy the data and defer its processing if it's heavy (to avoid stalling the internal goroutine).
- Library supports `context.Context` out of the box.
- You can provide custom `*http.Client`.
- You can even provide a custom prototype `*http.Request`. Before doing the request, the library `Clone()`s it.
- Default retry timeout is 1s (fixed). The library understands `retry` field of the protocol.
- The library will set `Last-Message-Id` header on request retries if `id` field was provided.
- The library automatically retries HTTP requests until `Close()` is called or parent context is cancelled.
- Finally, just take a look at the code yourself. If we don't count a custom line parsing buffer, the library fits into a single file of ~350 LOC.

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

# Configuration

1. Specify how to build the HTTP request:
   - `WithURL()` - the request will be constructed as `http.NewRequest("GET", url, nil)`
   - `WithRequest()` - provide the custom request
2. Specify what HTTP client to use (or `http.DefaultClient` will be used):
   - `WithClient()`
3. Specify the parent context (or `context.Background()` will be used):
   - `WithContext()`
4. Specify the callback to be invoked on every message:
   - `WithCallback()`
5. Specify custom buffer parameters, in case if you have custom memory requirements:
   - `WithBufferParameters()`
   - Defaults are:
     - 256 bytes for `id`/`event` fields.
     - 4MB for `data` field.
     - Read buffer is big enough to fit the largest field.
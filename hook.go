package sumologrus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/segmentio/backo-go"
	"github.com/sirupsen/logrus"
	"net/http"
	"os"
	"strings"
	"time"
)

// Backoff policy.
var Backo = backo.DefaultBacko()

type SumoLogicHook struct {
	endPointUrl string
	tags        []string
	host        string
	levels      []logrus.Level
	logger      *logrus.Logger
	verbose     bool
	interval    time.Duration
	size        int

	msgs     chan interface{}
	quit     chan struct{}
	shutdown chan struct{}
}

type SumoLogicMesssage struct {
	Tags  []string    `json:"tags"`
	Host  string      `json:"host"`
	Level string      `json:"level"`
	Data  interface{} `json:"data"`
}

var (
	newline = []byte{'\n'}
)

func NewSumoLogicHook(endPointUrl string, host string, level logrus.Level, tags ...string) *SumoLogicHook {
	levels := []logrus.Level{}
	log := logrus.New()
	log.Out = os.Stdout

	log.WithFields(logrus.Fields{
		"application": "sumologrus",
	})

	for _, l := range []logrus.Level{
		logrus.PanicLevel,
		logrus.FatalLevel,
		logrus.ErrorLevel,
		logrus.WarnLevel,
		logrus.InfoLevel,
		logrus.DebugLevel,
	} {
		if l <= level {
			levels = append(levels, l)
		}
	}

	var tagList []string
	for _, tag := range tags {
		tagList = append(tagList, tag)
	}

	hook := &SumoLogicHook{
		host:        host,
		tags:        tagList,
		endPointUrl: endPointUrl,
		levels:      levels,
		logger:      log,
		verbose:     false,
		interval:    5 * time.Second,
		size:        250,
		msgs:        make(chan interface{}, 100),
		quit:        make(chan struct{}),
		shutdown:    make(chan struct{}),
	}
	hook.startLoop()
	return hook
}

func (h *SumoLogicHook) Fire(entry *logrus.Entry) error {
	data := map[string]interface{}{
		"message": entry.Message,
		"fields":  entry.Data,
	}

	msg := SumoLogicMesssage{
		Tags:  h.tags,
		Host:  h.host,
		Level: strings.ToUpper(entry.Level.String()),
		Data:  data,
	}
	h.queue(msg)

	return nil
}

func (h *SumoLogicHook) queue(msg SumoLogicMesssage) {
	h.msgs <- msg
}

func (h *SumoLogicHook) upload(b []byte) error {
	payload := [][]byte{b}
	req, err := http.NewRequest(
		"POST",
		h.endPointUrl,
		bytes.NewBuffer(bytes.Join(payload, newline)),
	)
	if err != nil {
		fmt.Println("error creating sumologic request", err)
		return err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)

	if err != nil {
		return fmt.Errorf("error sending sumologic request: %s", err)
	}

	resp.Body.Close()
	return nil
}

// Send batch request.
func (h *SumoLogicHook) send(msgs []interface{}) error {
	if len(msgs) == 0 {
		return nil
	}

	b, err := json.Marshal(msgs)
	if err != nil {
		return fmt.Errorf("error marshalling msgs: %s", err)
	}

	if err = h.upload(b); err == nil {
		return nil
	}

	return err
}

func (h *SumoLogicHook) Levels() []logrus.Level {
	return h.levels
}

func (h *SumoLogicHook) Flush() error {
	h.quit <- struct{}{}
	close(h.msgs)
	<-h.shutdown
	return nil
}

func (h *SumoLogicHook) startLoop() {
	go h.loop()
}

func (h *SumoLogicHook) loop() {
	// Batch send the current log lines each Intervl
	tick := time.NewTicker(h.interval)
	var msgs []interface{}
	for {
		select {
		case msg := <-h.msgs:
			msgs = append(msgs, msg)
			if len(msgs) == h.size {
				h.log("exceeded %d messages – flushing", h.size)
				h.send(msgs)
				msgs = make([]interface{}, 0, h.size)
			}
		case <-tick.C:
			if len(msgs) > 0 {
				h.log("interval reached - flushing %d", len(msgs))
				h.send(msgs)
				msgs = make([]interface{}, 0, h.size)
			} else {
				h.log("interval reached – nothing to send")
			}
		case <-h.quit:
			tick.Stop()
			h.log("exit requested – draining msgs")
			// drain the msg channel.
			for msg := range h.msgs {
				h.log("buffer (%d/%d) %v", len(msgs), h.size, msg)
				msgs = append(msgs, msg)
			}
			h.log("exit requested – flushing %d", len(msgs))
			h.send(msgs)
			h.log("exit")
			h.shutdown <- struct{}{}
			return
		}

	}
}

func (h *SumoLogicHook) log(msg string, args ...interface{}) {
	if h.verbose {
		h.logger.Printf(msg, args...)
	}
}

func (h *SumoLogicHook) logf(msg string, args ...interface{}) {
	h.logger.Printf(msg, args...)
}

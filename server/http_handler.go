package server

import (
	"io"
	"net/http"
	"strconv"
	"sync"

	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/akmistry/dory"
)

var (
	requestCounter = prom.NewCounterVec(prom.CounterOpts{
		Name: "dory_http_requests_total",
		Help: "Total number of dory HTTP requests.",
	}, []string{"code", "method"})
)

func init() {
	prom.MustRegister(requestCounter)
}

type Handler struct {
	c       *dory.Memcache
	bufPool *sync.Pool
}

func (h *Handler) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	if req.URL.Path == "/" {
		resp.WriteHeader(http.StatusNotFound)
		return
	}

	key := []byte(req.URL.Path)
	if key[0] == '/' {
		key = key[1:]
	}
	if len(key) < h.c.MinKeySize() {
		resp.WriteHeader(http.StatusBadRequest)
		io.WriteString(resp, "Key too small")
		return
	} else if len(key) > h.c.MaxKeySize() {
		resp.WriteHeader(http.StatusBadRequest)
		io.WriteString(resp, "Key too large")
		return
	}

	switch req.Method {
	case "HEAD":
		// TODO: Return size.
		has := h.c.Has(key)
		if has {
			resp.WriteHeader(http.StatusOK)
		} else {
			resp.WriteHeader(http.StatusNotFound)
		}
	case "GET":
		buf := h.bufPool.Get().([]byte)[:0]
		defer h.bufPool.Put(buf)

		outBuf := h.c.Get(key, buf)
		if outBuf == nil {
			resp.WriteHeader(http.StatusNotFound)
			return
		}
		resp.Header().Add("Content-Length", strconv.Itoa(len(outBuf)))
		resp.Write(outBuf)
	case "PUT":
		if req.ContentLength < 0 {
			resp.WriteHeader(http.StatusLengthRequired)
			return
		} else if req.ContentLength < int64(h.c.MinValSize()) {
			resp.WriteHeader(http.StatusBadRequest)
			io.WriteString(resp, "Value too small")
			return
		} else if req.ContentLength > int64(h.c.MaxValSize()) {
			resp.WriteHeader(http.StatusBadRequest)
			io.WriteString(resp, "Value too large")
			return
		}

		buf := h.bufPool.Get().([]byte)
		defer h.bufPool.Put(buf)
		buf = buf[:cap(buf)]

		n, err := req.Body.Read(buf)
		if err != nil && err != io.EOF {
			resp.WriteHeader(http.StatusBadRequest)
			return
		} else if n != int(req.ContentLength) {
			resp.WriteHeader(http.StatusExpectationFailed)
			io.WriteString(resp, "Content read size != Content-Length header")
			return
		}
		h.c.Put(key, buf[:n])
	case "DELETE":
		h.c.Delete(key)
	case "PING":
		// Health-check method.
		resp.WriteHeader(http.StatusOK)
	default:
		resp.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func NewHandler(c *dory.Memcache) http.Handler {
	pool := &sync.Pool{New: func() interface{} { return make([]byte, c.MaxValSize()) }}
	return promhttp.InstrumentHandlerCounter(requestCounter, &Handler{
		c:       c,
		bufPool: pool,
	})
}

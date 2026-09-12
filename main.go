package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

const (
	defaultPublicPort      = "2053"
	defaultPanelPort       = "20530"
	defaultWSPort          = "20868"
	defaultXHTTPPort       = "20869"
	defaultGRPCPort        = "20870"
	defaultHTTPUpgradePort = "20871"
	defaultSubPort         = "2096"

	wsPrefix          = "/xvpnws/"
	xhttpPrefix       = "/xhttp/"
	grpcPrefix        = "/grpc/"
	httpUpgradePrefix = "/httpupgrade/"
	subPrefix         = "/sub/"
)

type byteBufferPool struct {
	pool sync.Pool
}

func newByteBufferPool() *byteBufferPool {
	p := &byteBufferPool{}
	p.pool.New = func() any {
		b := make([]byte, 32*1024)
		return &b
	}
	return p
}

func (p *byteBufferPool) Get() []byte {
	return *(p.pool.Get().(*[]byte))
}

func (p *byteBufferPool) Put(b []byte) {
	if cap(b) < 32*1024 {
		return
	}
	b = b[:32*1024]
	p.pool.Put(&b)
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func appendEnvDefault(env []string, key, value string) []string {
	if strings.TrimSpace(os.Getenv(key)) == "" {
		return append(env, key+"="+value)
	}
	return env
}

func newHTTPTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          512,
		MaxIdleConnsPerHost:   256,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableCompression:    true,
	}
}

func newHTTPProxy(target string, transport http.RoundTripper, pool httputil.BufferPool) *httputil.ReverseProxy {
	u, err := url.Parse(target)
	if err != nil {
		panic(err)
	}

	p := httputil.NewSingleHostReverseProxy(u)
	p.Transport = transport
	p.BufferPool = pool
	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		fmt.Fprintf(os.Stderr, "proxy error path=%s upstream=%s err=%v\n", r.URL.Path, target, err)
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}
	return p
}

func newGRPCProxy(port string, pool httputil.BufferPool) *httputil.ReverseProxy {
	transport := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, _ string, _ *tls.Config) (net.Conn, error) {
			return (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext(ctx, network, "127.0.0.1:"+port)
		},
	}

	p := &httputil.ReverseProxy{
		Director: func(r *http.Request) {
			r.URL.Scheme = "http"
			r.URL.Host = "127.0.0.1:" + port
			// Keep the original Host/SNI-facing hostname. Xray gRPC routing is
			// based on its service path; preserving Host is friendlier to proxies.
		},
		Transport:  transport,
		BufferPool: pool,
	}
	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		fmt.Fprintf(os.Stderr, "grpc proxy error path=%s upstream=127.0.0.1:%s err=%v\n", r.URL.Path, port, err)
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}
	return p
}

func startProxy(ctx context.Context) (*http.Server, error) {
	publicPort := envOr("PORT", defaultPublicPort)
	panelPort := envOr("XUI_PORT", defaultPanelPort)
	wsPort := envOr("XUI_WS_PORT", defaultWSPort)
	xhttpPort := envOr("XUI_XHTTP_PORT", defaultXHTTPPort)
	grpcPort := envOr("XUI_GRPC_PORT", defaultGRPCPort)
	httpUpgradePort := envOr("XUI_HTTPUPGRADE_PORT", defaultHTTPUpgradePort)
	subPort := envOr("XUI_SUB_PORT", defaultSubPort)

	pool := newByteBufferPool()
	transport := newHTTPTransport()

	panelProxy := newHTTPProxy("http://127.0.0.1:"+panelPort, transport, pool)
	wsProxy := newHTTPProxy("http://127.0.0.1:"+wsPort, transport, pool)
	xhttpProxy := newHTTPProxy("http://127.0.0.1:"+xhttpPort, transport, pool)
	httpUpgradeProxy := newHTTPProxy("http://127.0.0.1:"+httpUpgradePort, transport, pool)
	subProxy := newHTTPProxy("http://127.0.0.1:"+subPort, transport, pool)
	grpcProxy := newGRPCProxy(grpcPort, pool)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/_health":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok\n"))
		case strings.HasPrefix(r.URL.Path, wsPrefix):
			wsProxy.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, xhttpPrefix):
			xhttpProxy.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, grpcPrefix):
			grpcProxy.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, httpUpgradePrefix):
			httpUpgradeProxy.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, subPrefix):
			subProxy.ServeHTTP(w, r)
		default:
			panelProxy.ServeHTTP(w, r)
		}
	})

	server := &http.Server{
		Addr:              ":" + publicPort,
		Handler:           h2c.NewHandler(mux, &http2.Server{}),
		ReadHeaderTimeout: 15 * time.Second,
		// No WriteTimeout/IdleTimeout: WS/XHTTP/gRPC can be long-lived.
	}

	ln, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return nil, err
	}

	fmt.Printf("public proxy listening on :%s\n", publicPort)
	fmt.Printf("panel        /              -> 127.0.0.1:%s\n", panelPort)
	fmt.Printf("websocket    %-14s -> 127.0.0.1:%s\n", wsPrefix, wsPort)
	fmt.Printf("xhttp        %-14s -> 127.0.0.1:%s\n", xhttpPrefix, xhttpPort)
	fmt.Printf("grpc         %-14s -> h2c://127.0.0.1:%s\n", grpcPrefix, grpcPort)
	fmt.Printf("httpupgrade  %-14s -> 127.0.0.1:%s\n", httpUpgradePrefix, httpUpgradePort)
	fmt.Printf("subscription %-14s -> 127.0.0.1:%s\n", subPrefix, subPort)

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	go func() {
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "public proxy stopped:", err)
		}
	}()

	return server, nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if _, err := startProxy(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "failed to start public proxy:", err)
		os.Exit(1)
	}

	xuiBinary := envOr("XUI_BINARY", "/app/x-ui/x-ui")
	if st, err := os.Stat(xuiBinary); err != nil || st.IsDir() {
		fmt.Fprintf(os.Stderr, "3x-ui binary is missing at %s: %v\n", xuiBinary, err)
		os.Exit(1)
	}

	panelPort := envOr("XUI_PORT", defaultPanelPort)
	dataDir := envOr("XUI_DB_FOLDER", "/app/data")
	logDir := envOr("XUI_LOG_FOLDER", dataDir+"/logs")

	env := os.Environ()
	env = appendEnvDefault(env, "XUI_PORT", panelPort)
	env = appendEnvDefault(env, "XUI_DB_FOLDER", dataDir)
	env = appendEnvDefault(env, "XUI_LOG_FOLDER", logDir)
	env = appendEnvDefault(env, "XUI_SKIP_HSTS", "true")
	env = appendEnvDefault(env, "XUI_ENABLE_FAIL2BAN", "false")

	cmd := exec.CommandContext(ctx, xuiBinary)
	cmd.Dir = "/app/x-ui"
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = env

	fmt.Println("starting 3x-ui...")
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "failed to start 3x-ui:", err)
		os.Exit(1)
	}

	err := cmd.Wait()
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "3x-ui exited:", err)
		os.Exit(1)
	}
}

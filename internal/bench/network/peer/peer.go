package peer

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/LIHA-Research/lihacloud-bench/internal/model"
	"github.com/LIHA-Research/lihacloud-bench/internal/redact"
	"github.com/LIHA-Research/lihacloud-bench/internal/statistics"
)

const workloadVersion = "peer-tls-v1"

type Spec struct {
	Profile     string
	Target      string
	Fingerprint string
	Token       string
	Duration    time.Duration
	RTTSamples  int
	Comparable  bool
}

func DefaultSpec(profile, target, fingerprint, token string) Spec {
	if profile == "quick" {
		return Spec{Profile: profile, Target: target, Fingerprint: fingerprint, Token: token, Duration: 5 * time.Second, RTTSamples: 5, Comparable: false}
	}
	return Spec{Profile: profile, Target: target, Fingerprint: fingerprint, Token: token, Duration: 30 * time.Second, RTTSamples: 20, Comparable: true}
}

func Run(ctx context.Context, spec Spec) model.Benchmark {
	benchmark := model.Benchmark{
		ID: "network-peer", Category: "network-peer", Status: model.StatusSuccess,
		Engine: "https-tls-peer", EngineVersion: "1", WorkloadVersion: workloadVersion, Comparable: spec.Comparable,
		Parameters: map[string]any{"duration_seconds": spec.Duration.Seconds(), "rtt_samples": spec.RTTSamples, "streams": []int{1, 8}},
		Metrics:    []model.Metric{}, Warnings: []string{},
	}
	if strings.TrimSpace(spec.Target) == "" || strings.TrimSpace(spec.Fingerprint) == "" || spec.Token == "" {
		return fail(benchmark, "peer_configuration", errors.New("peer target, certificate fingerprint, and token are required"), spec.Target)
	}
	client, baseURL, err := client(spec.Target, spec.Fingerprint, spec.Token)
	if err != nil {
		return fail(benchmark, "peer_configuration", err, spec.Target)
	}
	rtt := make([]float64, 0, spec.RTTSamples)
	for index := 0; index < spec.RTTSamples; index++ {
		started := time.Now()
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/v1/rtt", nil)
		response, requestErr := client.Do(request)
		if requestErr != nil {
			return fail(benchmark, "peer_rtt_failed", requestErr, spec.Target)
		}
		_, copyErr := io.Copy(io.Discard, response.Body)
		closeErr := response.Body.Close()
		if response.StatusCode != http.StatusNoContent || errors.Join(copyErr, closeErr) != nil {
			return fail(benchmark, "peer_rtt_failed", fmt.Errorf("peer returned HTTP %d", response.StatusCode), spec.Target)
		}
		rtt = append(rtt, float64(time.Since(started).Microseconds())/1000)
	}
	mean, _, p50, p95, p99 := statistics.Summary(rtt)
	benchmark.Metrics = append(benchmark.Metrics,
		model.Metric{Name: "rtt_mean", Value: mean, Unit: "ms", Direction: "lower"},
		model.Metric{Name: "rtt_p50", Value: p50, Unit: "ms", Direction: "lower"},
		model.Metric{Name: "rtt_p95", Value: p95, Unit: "ms", Direction: "lower"},
		model.Metric{Name: "rtt_p99", Value: p99, Unit: "ms", Direction: "lower"},
	)
	for _, streams := range []int{1, 8} {
		upload, uploadErr := transfer(ctx, client, baseURL, http.MethodPost, streams, spec.Duration)
		if uploadErr != nil {
			return fail(benchmark, "peer_upload_failed", uploadErr, spec.Target)
		}
		download, downloadErr := transfer(ctx, client, baseURL, http.MethodGet, streams, spec.Duration)
		if downloadErr != nil {
			return fail(benchmark, "peer_download_failed", downloadErr, spec.Target)
		}
		benchmark.Metrics = append(benchmark.Metrics,
			model.Metric{Name: fmt.Sprintf("upload_%d_stream", streams), Value: float64(upload*8) / spec.Duration.Seconds() / 1_000_000, Unit: "Mbit/s", Direction: "higher"},
			model.Metric{Name: fmt.Sprintf("download_%d_stream", streams), Value: float64(download*8) / spec.Duration.Seconds() / 1_000_000, Unit: "Mbit/s", Direction: "higher"},
		)
	}
	return benchmark
}

func client(target, fingerprint, token string) (*http.Client, string, error) {
	if !strings.Contains(target, "://") {
		target = "https://" + target
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, "", errors.New("peer target must be an HTTPS host and port")
	}
	expected, err := hex.DecodeString(normalizeFingerprint(fingerprint))
	if err != nil || len(expected) != sha256.Size {
		return nil, "", errors.New("peer certificate fingerprint must be a SHA-256 fingerprint")
	}
	transport := &http.Transport{TLSClientConfig: pinnedTLSConfig(expected), ForceAttemptHTTP2: false}
	sessionBytes := make([]byte, 16)
	if _, err := rand.Read(sessionBytes); err != nil {
		return nil, "", fmt.Errorf("create peer session: %w", err)
	}
	return &http.Client{Transport: tokenTransport{base: transport, token: token, session: hex.EncodeToString(sessionBytes)}, Timeout: 2 * time.Minute}, strings.TrimRight(parsed.String(), "/"), nil
}

func transfer(ctx context.Context, client *http.Client, baseURL, method string, streams int, duration time.Duration) (uint64, error) {
	started := make(chan struct{})
	results := make(chan uint64, streams)
	errorsChannel := make(chan error, streams)
	for index := 0; index < streams; index++ {
		go func() {
			<-started
			phaseContext, cancel := context.WithTimeout(ctx, duration+15*time.Second)
			defer cancel()
			endpoint := fmt.Sprintf("%s/v1/%s?duration_ms=%d", baseURL, map[bool]string{true: "upload", false: "download"}[method == http.MethodPost], duration.Milliseconds())
			var body io.Reader
			if method == http.MethodPost {
				body = &deadlineReader{deadline: time.Now().Add(duration)}
			}
			request, _ := http.NewRequestWithContext(phaseContext, method, endpoint, body)
			response, err := client.Do(request)
			if err != nil {
				errorsChannel <- err
				return
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				errorsChannel <- fmt.Errorf("peer returned HTTP %d", response.StatusCode)
				return
			}
			if method == http.MethodPost {
				var value struct {
					Bytes uint64 `json:"bytes"`
				}
				if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
					errorsChannel <- err
					return
				}
				results <- value.Bytes
				return
			}
			written, err := io.Copy(io.Discard, response.Body)
			if err != nil {
				errorsChannel <- err
				return
			}
			results <- uint64(written)
		}()
	}
	close(started)
	var total uint64
	for index := 0; index < streams; index++ {
		select {
		case value := <-results:
			total += value
		case err := <-errorsChannel:
			return 0, err
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	return total, nil
}

type tokenTransport struct {
	base    http.RoundTripper
	token   string
	session string
}

func (transport tokenTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header.Set("Authorization", "Bearer "+transport.token)
	clone.Header.Set("X-LIHA-Bench-Session", transport.session)
	return transport.base.RoundTrip(clone)
}

type deadlineReader struct {
	deadline time.Time
}

func (reader *deadlineReader) Read(buffer []byte) (int, error) {
	if time.Now().After(reader.deadline) {
		return 0, io.EOF
	}
	for index := range buffer {
		buffer[index] = byte(index)
	}
	return len(buffer), nil
}

func fail(benchmark model.Benchmark, code string, err error, secrets ...string) model.Benchmark {
	benchmark.Status, benchmark.ErrorCode, benchmark.Error = model.StatusFailed, code, redact.String(err.Error(), secrets...)
	return benchmark
}

func normalizeFingerprint(value string) string {
	value = strings.ReplaceAll(value, ":", "")
	value = strings.ReplaceAll(value, " ", "")
	return strings.ToLower(value)
}

// The client intentionally uses InsecureSkipVerify together with exact leaf
// certificate pinning; normal PKI validation cannot validate the ephemeral,
// self-signed peer certificate.
func pinnedTLSConfig(expected []byte) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13, InsecureSkipVerify: true, //nolint:gosec
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) != 1 {
				return errors.New("peer presented an unexpected certificate chain")
			}
			actual := sha256.Sum256(rawCerts[0])
			if subtle.ConstantTimeCompare(actual[:], expected) != 1 {
				return errors.New("peer certificate fingerprint mismatch")
			}
			return nil
		},
	}
}

type Server struct {
	Listen  string
	Token   string
	mutex   sync.Mutex
	session string
}

func (server *Server) Serve(ctx context.Context, output io.Writer) error {
	if server.Token == "" {
		return errors.New("peer token is required")
	}
	if server.Listen == "" {
		server.Listen = ":8443"
	}
	certificate, fingerprint, err := ephemeralCertificate()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", server.Listen)
	if err != nil {
		return err
	}
	tlsListener := tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS13})
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/rtt", server.authorize(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) }))
	mux.HandleFunc("/v1/download", server.authorize(downloadHandler))
	mux.HandleFunc("/v1/upload", server.authorize(uploadHandler))
	httpServer := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 2 * time.Minute}
	_, _ = fmt.Fprintf(output, "Peer server listening on %s\nCertificate fingerprint: %s\n", listener.Addr(), fingerprint)
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- httpServer.Serve(tlsListener) }()
	select {
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownContext)
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (server *Server) authorize(next http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		provided := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(provided), []byte(server.Token)) != 1 {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		session := request.Header.Get("X-LIHA-Bench-Session")
		server.mutex.Lock()
		if server.session == "" && session != "" {
			server.session = session
		}
		accepted := session != "" && subtle.ConstantTimeCompare([]byte(session), []byte(server.session)) == 1
		server.mutex.Unlock()
		if !accepted {
			http.Error(writer, "peer token has already been consumed", http.StatusUnauthorized)
			return
		}
		next(writer, request)
	}
}

func downloadHandler(writer http.ResponseWriter, request *http.Request) {
	duration, err := requestDuration(request)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	writer.Header().Set("Content-Type", "application/octet-stream")
	buffer := make([]byte, 256*1024)
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		if _, err := writer.Write(buffer); err != nil {
			return
		}
	}
}

func uploadHandler(writer http.ResponseWriter, request *http.Request) {
	if _, err := requestDuration(request); err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	bytes, err := io.Copy(io.Discard, request.Body)
	if err != nil {
		http.Error(writer, "upload failed", http.StatusBadRequest)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(struct {
		Bytes uint64 `json:"bytes"`
	}{Bytes: uint64(bytes)})
}

func requestDuration(request *http.Request) (time.Duration, error) {
	value, err := time.ParseDuration(request.URL.Query().Get("duration_ms") + "ms")
	if err != nil || value < time.Second || value > 35*time.Second {
		return 0, errors.New("duration must be between 1s and 35s")
	}
	return value, nil
}

func ephemeralCertificate() (tls.Certificate, string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, "", err
	}
	now := time.Now()
	template := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "lihacloud-bench-peer"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	keyBytes, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	certificate, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes}))
	if err != nil {
		return tls.Certificate{}, "", err
	}
	digest := sha256.Sum256(der)
	encoded := strings.ToUpper(hex.EncodeToString(digest[:]))
	parts := make([]string, 0, sha256.Size)
	for index := 0; index < len(encoded); index += 2 {
		parts = append(parts, encoded[index:index+2])
	}
	return certificate, strings.Join(parts, ":"), nil
}

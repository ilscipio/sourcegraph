package httpcli

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"testing/quick"
	"time"

	"github.com/PuerkitoBio/rehttp"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"

	"github.com/sourcegraph/sourcegraph/internal/actor"
	"github.com/sourcegraph/sourcegraph/lib/errors"
)

func TestHeadersMiddleware(t *testing.T) {
	headers := []string{"X-Foo", "bar", "X-Bar", "foo"}
	for _, tc := range []struct {
		name    string
		cli     Doer
		headers []string
		err     string
	}{
		{
			name:    "odd number of headers panics",
			headers: headers[:1],
			cli: DoerFunc(func(r *http.Request) (*http.Response, error) {
				t.Fatal("should not be called")
				return nil, nil
			}),
			err: "missing header values",
		},
		{
			name:    "even number of headers are set",
			headers: headers,
			cli: DoerFunc(func(r *http.Request) (*http.Response, error) {
				for i := 0; i < len(headers); i += 2 {
					name := headers[i]
					if have, want := r.Header.Get(name), headers[i+1]; have != want {
						t.Errorf("header %q: have: %q, want: %q", name, have, want)
					}
				}
				return nil, nil
			}),
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if tc.err == "" {
				tc.err = "<nil>"
			}

			defer func() {
				if err := recover(); err != nil {
					if have, want := fmt.Sprint(err), tc.err; have != want {
						t.Fatalf("have error: %q\nwant error: %q", have, want)
					}
				}
			}()

			cli := HeadersMiddleware(tc.headers...)(tc.cli)
			req, _ := http.NewRequest("GET", "http://dev/null", nil)

			_, err := cli.Do(req)
			if have, want := fmt.Sprint(err), tc.err; have != want {
				t.Fatalf("have error: %q\nwant error: %q", have, want)
			}
		})
	}
}

func TestContextErrorMiddleware(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	for _, tc := range []struct {
		name string
		cli  Doer
		ctx  context.Context
		err  string
	}{
		{
			name: "no context error, no doer error",
			cli:  newFakeClient(http.StatusOK, nil, nil),
			err:  "<nil>",
		},
		{
			name: "no context error, with doer error",
			cli:  newFakeClient(http.StatusOK, nil, errors.New("boom")),
			err:  "boom",
		},
		{
			name: "with context error and no doer error",
			cli:  newFakeClient(http.StatusOK, nil, nil),
			ctx:  cancelled,
			err:  "<nil>",
		},
		{
			name: "with context error and doer error",
			cli:  newFakeClient(http.StatusOK, nil, errors.New("boom")),
			ctx:  cancelled,
			err:  context.Canceled.Error(),
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cli := ContextErrorMiddleware(tc.cli)

			req, _ := http.NewRequest("GET", "http://dev/null", nil)

			if tc.ctx != nil {
				req = req.WithContext(tc.ctx)
			}

			_, err := cli.Do(req)

			if have, want := fmt.Sprint(err), tc.err; have != want {
				t.Fatalf("have error: %q\nwant error: %q", have, want)
			}
		})
	}
}

func genCert(subject string) (string, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		return "", err
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{subject},
		},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	if err := pem.Encode(&b, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		return "", err
	}
	return b.String(), nil
}

func TestNewCertPool(t *testing.T) {
	subject := "newcertpooltest"
	cert, err := genCert(subject)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		certs  []string
		cli    *http.Client
		assert func(testing.TB, *http.Client)
		err    string
	}{
		{
			name:  "fails if transport isn't an http.Transport",
			cli:   &http.Client{Transport: bogusTransport{}},
			certs: []string{cert},
			err:   "httpcli.NewCertPoolOpt: http.Client.Transport cannot be cast as a *http.Transport: httpcli.bogusTransport",
		},
		{
			name:  "pool is set to what is given",
			cli:   &http.Client{Transport: &http.Transport{}},
			certs: []string{cert},
			assert: func(t testing.TB, cli *http.Client) {
				pool := cli.Transport.(*http.Transport).TLSClientConfig.RootCAs
				for _, have := range pool.Subjects() {
					if bytes.Contains(have, []byte(subject)) {
						return
					}
				}
				t.Fatal("could not find subject in pool")
			},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := NewCertPoolOpt(tc.certs...)(tc.cli)

			if tc.err == "" {
				tc.err = "<nil>"
			}

			if have, want := fmt.Sprint(err), tc.err; have != want {
				t.Fatalf("have error: %q\nwant error: %q", have, want)
			}

			if tc.assert != nil {
				tc.assert(t, tc.cli)
			}
		})
	}
}

func TestNewIdleConnTimeoutOpt(t *testing.T) {
	timeout := 33 * time.Second

	// originalRoundtripper must only be used in one test, set at this scope for
	// convenience.
	originalRoundtripper := &http.Transport{}

	for _, tc := range []struct {
		name    string
		cli     *http.Client
		timeout time.Duration
		assert  func(testing.TB, *http.Client)
		err     string
	}{
		{
			name: "sets default transport if nil",
			cli:  &http.Client{},
			assert: func(t testing.TB, cli *http.Client) {
				if cli.Transport == nil {
					t.Fatal("transport wasn't set")
				}
			},
		},
		{
			name: "fails if transport isn't an http.Transport",
			cli:  &http.Client{Transport: bogusTransport{}},
			err:  "httpcli.NewIdleConnTimeoutOpt: http.Client.Transport cannot be cast as a *http.Transport: httpcli.bogusTransport",
		},
		{
			name:    "IdleConnTimeout is set to what is given",
			cli:     &http.Client{Transport: &http.Transport{}},
			timeout: timeout,
			assert: func(t testing.TB, cli *http.Client) {
				have := cli.Transport.(*http.Transport).IdleConnTimeout
				if want := timeout; !reflect.DeepEqual(have, want) {
					t.Fatal(cmp.Diff(have, want))
				}
			},
		},
		{
			name: "IdleConnTimeout is set to what is given on a wrapped transport",
			cli: func() *http.Client {
				return &http.Client{Transport: &wrappedTransport{
					RoundTripper: &actor.HTTPTransport{RoundTripper: originalRoundtripper},
					Wrapped:      originalRoundtripper,
				}}
			}(),
			timeout: timeout,
			assert: func(t testing.TB, cli *http.Client) {
				unwrapped := unwrapAll(cli.Transport.(WrappedTransport))
				have := (*unwrapped).(*http.Transport).IdleConnTimeout

				// Timeout is set on the underlying transport
				if want := timeout; !reflect.DeepEqual(have, want) {
					t.Fatal(cmp.Diff(have, want))
				}

				// Original roundtripper unchanged!
				assert.Equal(t, time.Duration(0), originalRoundtripper.IdleConnTimeout)
			},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := NewIdleConnTimeoutOpt(tc.timeout)(tc.cli)

			if tc.err == "" {
				tc.err = "<nil>"
			}

			if have, want := fmt.Sprint(err), tc.err; have != want {
				t.Fatalf("have error: %q\nwant error: %q", have, want)
			}

			if tc.assert != nil {
				tc.assert(t, tc.cli)
			}
		})
	}
}

func TestNewTimeoutOpt(t *testing.T) {
	var cli http.Client

	timeout := 42 * time.Second
	err := NewTimeoutOpt(timeout)(&cli)
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}

	if have, want := cli.Timeout, timeout; have != want {
		t.Errorf("have Timeout %s, want %s", have, want)
	}
}

func TestErrorResilience(t *testing.T) {
	failures := int64(5)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := 0
		switch n := atomic.AddInt64(&failures, -1); n {
		case 4:
			status = 429
		case 3:
			status = 500
		case 2:
			status = 900
		case 1:
			status = 302
			w.Header().Set("Location", "/")
		case 0:
			status = 404
		}
		w.WriteHeader(status)
	}))

	t.Cleanup(srv.Close)

	req, err := http.NewRequest("GET", srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("many", func(t *testing.T) {
		cli, _ := NewFactory(
			NewMiddleware(
				ContextErrorMiddleware,
			),
			NewErrorResilientTransportOpt(
				NewRetryPolicy(20),
				rehttp.ExpJitterDelay(50*time.Millisecond, 5*time.Second),
			),
		).Doer()

		res, err := cli.Do(req)
		if err != nil {
			t.Fatal(err)
		}

		if res.StatusCode != 404 {
			t.Fatalf("want status code 404, got: %d", res.StatusCode)
		}
	})

	t.Run("max", func(t *testing.T) {
		atomic.StoreInt64(&failures, 5)

		cli, _ := NewFactory(
			NewMiddleware(
				ContextErrorMiddleware,
			),
			NewErrorResilientTransportOpt(
				NewRetryPolicy(0), // zero retries
				rehttp.ExpJitterDelay(50*time.Millisecond, 5*time.Second),
			),
		).Doer()

		res, err := cli.Do(req)
		if err != nil {
			t.Fatal(err)
		}

		if res.StatusCode != 429 {
			t.Fatalf("want status code 429, got: %d", res.StatusCode)
		}
	})

	t.Run("no such host", func(t *testing.T) {
		// spy on policy so we see what decisions it makes
		retries := 0
		policy := NewRetryPolicy(5) // smaller retries for faster failures
		wrapped := func(a rehttp.Attempt) bool {
			if policy(a) {
				retries++
				return true
			}
			return false
		}

		cli, _ := NewFactory(
			NewMiddleware(
				ContextErrorMiddleware,
			),
			func(cli *http.Client) error {
				// Some DNS servers do not respect RFC 6761 section 6.4, so we
				// hardcode what go returns for DNS not found to avoid
				// flakiness across machines. However, CI correctly respects
				// this so we continue to run against a real DNS server on CI.
				if os.Getenv("CI") == "" {
					cli.Transport = notFoundTransport{}
				}
				return nil
			},
			NewErrorResilientTransportOpt(
				wrapped,
				rehttp.ExpJitterDelay(50*time.Millisecond, 5*time.Second),
			),
		).Doer()

		// requests to .invalid will fail DNS lookup. (RFC 6761 section 6.4)
		req, err := http.NewRequest("GET", "http://test.invalid", nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = cli.Do(req)

		var dnsErr *net.DNSError
		if !errors.As(err, &dnsErr) || !dnsErr.IsNotFound {
			t.Fatalf("expected err to be net.DNSError with IsNotFound true: %v", err)
		}

		// policy is on DNS failure to retry 3 times
		if want := 3; retries != want {
			t.Fatalf("expected %d retries, got %d", want, retries)
		}
	})
}

type notFoundTransport struct{}

func (notFoundTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, &net.DNSError{IsNotFound: true}
}

func TestExpJitterDelay(t *testing.T) {
	prop := func(b, m uint32, a uint16) bool {
		base := time.Duration(b)
		max := time.Duration(m)
		for max < base {
			max *= 2
		}
		attempt := int(a)

		delay := ExpJitterDelay(base, max)(rehttp.Attempt{
			Index: attempt,
		})

		t.Logf("base: %v, max: %v, attempt: %v", base, max, attempt)

		switch {
		case delay > max:
			t.Logf("delay %v > max %v", delay, max)
			return false
		case delay < base:
			t.Logf("delay %v < base %v", delay, base)
			return false
		}

		return true
	}

	err := quick.Check(prop, nil)
	if err != nil {
		t.Fatal(err)
	}
}

//nolint:unparam // unparam complains that `code` always has same value across call-sites, but that's OK
func newFakeClient(code int, body []byte, err error) Doer {
	return DoerFunc(func(r *http.Request) (*http.Response, error) {
		rr := httptest.NewRecorder()
		_, _ = rr.Write(body)
		rr.WriteHeader(code)
		return rr.Result(), err
	})
}

type bogusTransport struct{}

func (t bogusTransport) RoundTrip(*http.Request) (*http.Response, error) {
	panic("should not be called")
}

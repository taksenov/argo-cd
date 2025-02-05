package dex

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strconv"

	log "github.com/sirupsen/logrus"

	"github.com/argoproj/argo-cd/v2/util/errors"
)

func decorateDirector(director func(req *http.Request), target *url.URL) func(req *http.Request) {
	return func(req *http.Request) {
		director(req)
		req.Host = target.Host
	}
}

// NewDexHTTPReverseProxy returns a reverse proxy to the Dex server. Dex is assumed to be configured
// with the external issuer URL muxed to the same path configured in server.go. In other words, if
// Argo CD API server wants to proxy requests at /api/dex, then the dex config yaml issuer URL should
// also be /api/dex (e.g. issuer: https://argocd.example.com/api/dex)
func NewDexHTTPReverseProxy(serverAddr string, baseHRef string) func(writer http.ResponseWriter, request *http.Request) {
	target, err := url.Parse(serverAddr)
	errors.CheckError(err)
	target.Path = baseHRef

	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ModifyResponse = func(resp *http.Response) error {
		if resp.StatusCode == 500 {
			b, err := io.ReadAll(resp.Body)
			if err != nil {
				return err
			}
			err = resp.Body.Close()
			if err != nil {
				return err
			}
			log.Errorf("received error from dex: %s", string(b))
			resp.ContentLength = 0
			resp.Header.Set("Content-Length", strconv.Itoa(0))
			resp.Header.Set("Location", fmt.Sprintf("%s?has_sso_error=true", path.Join(baseHRef, "login")))
			resp.StatusCode = http.StatusSeeOther
			resp.Body = io.NopCloser(bytes.NewReader(make([]byte, 0)))
			return nil
		}
		return nil
	}
	proxy.Director = decorateDirector(proxy.Director, target)
	return func(w http.ResponseWriter, r *http.Request) {
		proxy.ServeHTTP(w, r)
	}
}

// NewDexRewriteURLRoundTripper creates a new DexRewriteURLRoundTripper
func NewDexRewriteURLRoundTripper(dexServerAddr string, T http.RoundTripper) DexRewriteURLRoundTripper {
	dexURL, _ := url.Parse(dexServerAddr)
	return DexRewriteURLRoundTripper{
		DexURL: dexURL,
		T:      T,
	}
}

// DexRewriteURLRoundTripper is an HTTP RoundTripper to rewrite HTTP requests to the specified
// dex server address. This is used when reverse proxying Dex to avoid the API server from
// unnecessarily communicating to Argo CD through its externally facing load balancer, which is not
// always permitted in firewalled/air-gapped networks.
type DexRewriteURLRoundTripper struct {
	DexURL *url.URL
	T      http.RoundTripper
}

func (s DexRewriteURLRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	r.URL.Host = s.DexURL.Host
	r.URL.Scheme = s.DexURL.Scheme
	return s.T.RoundTrip(r)
}

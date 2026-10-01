package promapi

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"

	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
)

func isUnsupportedError(err error) bool {
	if e1, ok := errors.AsType[APIError](err); ok {
		return e1.ErrorType == ErrAPIUnsupported
	}
	return false
}

func IsUnavailableError(err error) bool {
	if e1, ok := errors.AsType[APIError](err); ok {
		return e1.ErrorType == v1.ErrServer
	}
	if _, ok := errors.AsType[QueryError](err); ok {
		return true
	}
	return false
}

func IsQueryTooExpensive(err error) bool {
	if e1, ok := errors.AsType[APIError](err); ok {
		if e1.ErrorType != v1.ErrExec {
			return false
		}
		if strings.HasPrefix(e1.Err.Error(), "query processing would load too many samples into memory in ") {
			return true
		}
		if strings.HasSuffix(e1.Err.Error(), "expanding series: context deadline exceeded") {
			return true
		}
		// VictoriaMetrics query limits.
		if strings.Contains(e1.Err.Error(), "-search.maxSamplesPerQuery") || strings.Contains(e1.Err.Error(), "-search.maxUniqueTimeseries") {
			return true
		}
	}
	return false
}

type APIError struct {
	Err       error        `json:"error"`
	Status    string       `json:"status"`
	ErrorType v1.ErrorType `json:"errorType"`
}

func (e APIError) Error() string {
	return e.Err.Error()
}

func (e APIError) Unwrap() error {
	return e.Err
}

const (
	ErrUnknown        v1.ErrorType = "unknown"
	ErrJSONStream     v1.ErrorType = "json_stream"
	ErrAPIUnsupported v1.ErrorType = "unsupported"
)

func decodeErrorType(s string) v1.ErrorType {
	switch s {
	case string(v1.ErrBadData):
		return v1.ErrBadData
	case string(v1.ErrTimeout):
		return v1.ErrTimeout
	case string(v1.ErrCanceled):
		return v1.ErrCanceled
	case string(v1.ErrExec):
		return v1.ErrExec
	case string(v1.ErrBadResponse):
		return v1.ErrBadResponse
	case string(v1.ErrServer):
		return v1.ErrServer
	case string(v1.ErrClient):
		return v1.ErrClient
	default:
		return decodeHTTPErrorType(s)
	}
}

// decodeHTTPErrorType handles VictoriaMetrics responses which use HTTP status codes as errorType.
func decodeHTTPErrorType(s string) v1.ErrorType {
	code, err := strconv.Atoi(s)
	if err != nil {
		return ErrUnknown
	}
	switch {
	case code == http.StatusBadRequest:
		return v1.ErrBadData
	case code == http.StatusUnprocessableEntity:
		return v1.ErrExec
	case code == http.StatusGatewayTimeout:
		return v1.ErrTimeout
	case code/100 == 4:
		return v1.ErrClient
	case code/100 == 5:
		return v1.ErrServer
	default:
		return ErrUnknown
	}
}

const (
	errConnRefused = "connection refused"
	errConnTimeout = "connection timeout"
)

func decodeError(err error) string {
	if errors.Is(err, context.Canceled) {
		return context.Canceled.Error()
	}

	if errors.Is(err, syscall.ECONNREFUSED) {
		return errConnRefused
	}

	if neterr, ok := errors.AsType[net.Error](err); ok && neterr.Timeout() {
		return errConnTimeout
	}

	if e1, ok := errors.AsType[APIError](err); ok {
		return string(e1.ErrorType) + ": " + e1.Err.Error()
	}

	return err.Error()
}

const maxErrorBodySize = 1 << 20

type PrometheusResponse struct {
	Status    string `json:"status"`
	Error     string `json:"error"`
	ErrorType string `json:"errorType"`
	// IsPartial is set by VictoriaMetrics cluster when some vmstorage nodes didn't respond.
	IsPartial bool `json:"isPartial"`
}

// partialResponseError rejects partial VictoriaMetrics results, checks would otherwise
// report series missing on unavailable storage nodes as rule problems.
func (pr PrometheusResponse) partialResponseError() error {
	if !pr.IsPartial {
		return nil
	}
	return APIError{
		Status:    pr.Status,
		ErrorType: v1.ErrServer,
		Err:       errors.New("VictoriaMetrics returned a partial response, some vmstorage nodes are unavailable"),
	}
}

func tryDecodingAPIError(resp *http.Response) error {
	slog.LogAttrs(context.Background(), slog.LevelDebug, "Trying to parse Prometheus error response", slog.Int("code", resp.StatusCode))

	if resp.StatusCode == http.StatusNotFound {
		var apiPath string
		msg := "some API endpoints"
		if resp.Request != nil {
			switch {
			case strings.HasSuffix(resp.Request.URL.Path, APIPathConfig):
				apiPath = APIPathConfig
			case strings.HasSuffix(resp.Request.URL.Path, APIPathFlags):
				apiPath = APIPathFlags
			case strings.HasSuffix(resp.Request.URL.Path, APIPathMetadata):
				apiPath = APIPathMetadata
			case strings.HasSuffix(resp.Request.URL.Path, APIPathBuildInfo):
				apiPath = APIPathBuildInfo
			case strings.HasSuffix(resp.Request.URL.Path, vmPathFlags):
				apiPath = vmPathFlags
			}
			msg = "`" + apiPath + "` API endpoint"
		}
		if apiPath != "" {
			return APIError{
				Status:    "",
				ErrorType: ErrAPIUnsupported,
				Err:       errors.New("this server doesn't seem to support " + msg),
			}
		}
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))

	var data PrometheusResponse
	if err := json.Unmarshal(body, &data); err != nil {
		switch resp.StatusCode / 100 {
		case 4:
			// VictoriaMetrics returns plain text errors for unknown API paths.
			if bytes.Contains(body, []byte("unsupported path requested")) {
				msg := "some API endpoints"
				if resp.Request != nil {
					msg = "`" + resp.Request.URL.Path + "` API endpoint"
				}
				return APIError{
					Status:    "",
					ErrorType: ErrAPIUnsupported,
					Err:       errors.New("this server doesn't seem to support " + msg),
				}
			}
			return APIError{
				Status:    "error",
				ErrorType: v1.ErrClient,
				Err:       errors.New(resp.Status),
			}
		case 5:
			return APIError{
				Status:    "error",
				ErrorType: v1.ErrServer,
				Err:       errors.New(resp.Status),
			}
		}
		return APIError{
			Status:    "error",
			ErrorType: v1.ErrBadResponse,
			Err:       errors.New(resp.Status),
		}
	}

	return APIError{
		Status:    data.Status,
		ErrorType: decodeErrorType(data.ErrorType),
		Err:       errors.New(data.Error),
	}
}

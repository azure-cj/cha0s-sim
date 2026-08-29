package chaos

import (
	"io"
	"net/http"
	"strconv"
	"strings"

	"cha0s-sim/internal/config"
)

type StatusInjector struct {
	Code      int
	StripBody bool
}

func NewStatusInjector(cfg *config.StatusConfig) *StatusInjector {
	return &StatusInjector{
		Code:      cfg.Code,
		StripBody: cfg.StripBody,
	}
}

func (si *StatusInjector) InjectResponse(resp *http.Response) error {
	resp.StatusCode = si.Code

	text := http.StatusText(si.Code)
	if text == "" {
		resp.Status = strconv.Itoa(si.Code)
	} else {
		resp.Status = strconv.Itoa(si.Code) + " " + text
	}

	if si.StripBody {
		if resp.Body != nil {
			resp.Body.Close()
		}
		resp.Body = io.NopCloser(strings.NewReader(""))
		resp.ContentLength = 0
		resp.Header.Set("Content-Length", "0")
	}

	return nil
}

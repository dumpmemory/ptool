package transmissionrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"sync"
)

const csrfHeader = "X-Transmission-Session-Id"

type requestPayload struct {
	Method    string      `json:"method"`
	Arguments interface{} `json:"arguments,omitempty"`
	Tag       int         `json:"tag,omitempty"`
}

type answerPayload struct {
	Arguments interface{} `json:"arguments"`
	Result    string      `json:"result"`
	Tag       *int        `json:"tag"`
}

func (c *Client) rpcCall(ctx context.Context, method string, arguments interface{}, result interface{}) (err error) {
	return c.request(ctx, method, arguments, result, true)
}

func (c *Client) request(ctx context.Context, method string, arguments interface{}, result interface{}, retry bool) (err error) {
	// Let's avoid crashing
	if c.httpC == nil {
		err = errors.New("this controller is not initialized, please use the New() function")
		return
	}
	// Prepare the pipeline between payload generation and request
	pOut, pIn := io.Pipe()
	// Prepare the request
	var req *http.Request
	if req, err = http.NewRequestWithContext(ctx, "POST", c.url, pOut); err != nil {
		err = fmt.Errorf("can't prepare request for '%s' method: %w", method, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set(csrfHeader, c.getSessionID())
	req.SetBasicAuth(c.user, c.password)
	// Prepare the marshalling goroutine
	var tag int
	var encErr error
	var mg sync.WaitGroup
	mg.Add(1)
	go func() {
		tag = c.rnd.Int()
		encErr = json.NewEncoder(pIn).Encode(&requestPayload{
			Method:    method,
			Arguments: arguments,
			Tag:       tag,
		})
		pIn.Close()
		mg.Done()
	}()
	// Execute request
	var resp *http.Response
	if resp, err = c.httpC.Do(req); err != nil {
		mg.Wait()
		if encErr != nil {
			err = fmt.Errorf("request error: %w | json payload marshall error: %w", err, encErr)
		} else {
			err = fmt.Errorf("request error: %w", err)
		}
		return
	}
	defer resp.Body.Close()
	// Let's test the enc result, just in case
	mg.Wait()
	if encErr != nil {
		err = fmt.Errorf("request payload JSON marshalling failed: %w", encErr)
		return
	}
	// Is the CRSF token invalid ?
	if resp.StatusCode == http.StatusConflict {
		// Recover new token and save it
		c.updateSessionID(resp.Header.Get(csrfHeader))
		// Retry request if first try
		if retry {
			return c.request(ctx, method, arguments, result, false)
		}
		err = errors.New("CSRF token invalid 2 times in a row: stopping to avoid infinite loop")
		return
	}
	// Is request successful ?
	if resp.StatusCode != 200 {
		err = HTTPStatusCode(resp.StatusCode)
		return
	}
	// Debug
	if c.debug {
		var data []byte
		if data, err = ioutil.ReadAll(resp.Body); err == nil {
			fmt.Fprintln(os.Stderr, string(data))
		} else {
			fmt.Fprintln(os.Stderr, err.Error())
		}
		resp.Body.Close()
		resp.Body = ioutil.NopCloser(bytes.NewBuffer(data))
	}
	// Decode body
	answer := answerPayload{
		Arguments: result,
	}
	if err = json.NewDecoder(resp.Body).Decode(&answer); err != nil {
		err = fmt.Errorf("can't unmarshall request answer body: %w", err)
		return
	}
	// fmt.Println("DEBUG >", answer.Result)
	// Final checks
	if answer.Tag == nil {
		err = errors.New("http answer does not have a tag within it's payload")
		return
	}
	if *answer.Tag != tag {
		err = errors.New("http request tag and answer payload tag do not match")
		return
	}
	if answer.Result != "success" {
		err = fmt.Errorf("http request ok but payload does not indicate success: %s", answer.Result)
		return
	}
	// All good
	return
}

func (c *Client) getSessionID() string {
	defer c.sessionIDAccess.RUnlock()
	c.sessionIDAccess.RLock()
	return c.sessionID
}

func (c *Client) updateSessionID(newID string) {
	defer c.sessionIDAccess.Unlock()
	c.sessionIDAccess.Lock()
	c.sessionID = newID
}

// HTTPStatusCode is a custom error type for HTTP errors
type HTTPStatusCode int

func (hsc HTTPStatusCode) Error() string {
	text := http.StatusText(int(hsc))
	if text != "" {
		text = ": " + text
	}
	return fmt.Sprintf("HTTP error %d%s", hsc, text)
}

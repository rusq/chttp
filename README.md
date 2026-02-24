# chttp

[![Go Reference](https://pkg.go.dev/badge/github.com/rusq/chttp.svg)][1]

Cooked HTTP — a standard golang HTTP Client wrapper that adds a cookie jar
with user-defined cookies, and a customised transport.

Simple usage:
```go
import "github.com/rusq/chttp"

func getSomething() error {
	cookies := readFromFile()
	cl, err := chttp.New("https://slack.com", cookies)
	if err != nil {
		return err
	}

	resp, err := cl.Get("url") // executes with cookies from the jar
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// do something with resp
	return nil
}
```

See [package documentation][1] if you'd like to read more. 

[1]: https://pkg.go.dev/github.com/rusq/chttp

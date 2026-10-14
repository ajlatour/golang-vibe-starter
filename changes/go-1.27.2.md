### Security

- Built with Go 1.27.2, which fixes nine vulnerabilities in Go 1.27.0's standard library that
  govulncheck found the service's code can reach: in `net/http` (HTTP/2 flow control, HPACK, trailers and
  framing, and HTTP/1 connection desynchronization after `CONNECT`), `mime/multipart` and
  `crypto/tls` (GO-2026-6603 to GO-2026-6617).

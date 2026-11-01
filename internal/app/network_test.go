package app

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSubscriptionUpdateIsValidatedAndPrivate(t *testing.T) {
	f := newFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/valid":
			fmt.Fprint(w, subscriptionFixture)
		case "/secret-token/status":
			w.WriteHeader(403)
		case "/secret-token/invalid":
			fmt.Fprint(w, "<html>not a subscription</html>")
		case "/empty":
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	for _, path := range []string{"/secret-token/status", "/secret-token/invalid", "/empty"} {
		r := f.call("update", server.URL+path)
		if r.code == 0 {
			t.Fatal("invalid subscription accepted", path)
		}
		if strings.Contains(r.err, "secret-token") {
			t.Fatal("URL credentials printed")
		}
		data, _ := os.ReadFile(filepath.Join(f.data, "config.yaml"))
		if string(data) != subscriptionFixture {
			t.Fatal("last usable subscription overwritten")
		}
	}
	f.set("PROXRUN_SUBSCRIPTION", server.URL+"/valid")
	f.require(f.call("update"), 0)
	info, err := os.Stat(filepath.Join(f.data, "config.yaml"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("subscription permissions", info, err)
	}
	files, _ := filepath.Glob(filepath.Join(f.data, ".proxrun-*.tmp"))
	if len(files) != 0 {
		t.Fatal("temporary credentials retained")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestKernelDownloadAtomicAndFailure(t *testing.T) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	writer.Write([]byte("#!/bin/sh\necho test-kernel\n"))
	writer.Close()
	path := filepath.Join(t.TempDir(), "mihomo")
	os.WriteFile(path, []byte("old-kernel"), 0755)
	mode := "valid"
	var requested string
	a := New(nil, io.Discard, io.Discard)
	a.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requested = r.URL.Path
		status := 200
		data := compressed.Bytes()
		if mode == "status" {
			status = 404
		}
		if mode == "gzip" {
			data = []byte("bad gzip")
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header)}, nil
	})}
	c := Config{MihomoBin: path, MihomoVersion: "1.19.33"}
	for _, bad := range []string{"status", "gzip"} {
		mode = bad
		if err := a.downloadKernel(c); err == nil {
			t.Fatal("download falsely succeeded")
		}
		data, _ := os.ReadFile(path)
		if string(data) != "old-kernel" {
			t.Fatal("working kernel overwritten")
		}
	}
	mode = "valid"
	if err := a.downloadKernel(c); err != nil {
		t.Fatal(err)
	}
	asset, _ := kernelAsset(runtime.GOOS, runtime.GOARCH, c.MihomoVersion)
	if !strings.HasSuffix(requested, "/"+asset) {
		t.Fatal(requested)
	}
	data, _ := os.ReadFile(path)
	if !bytes.Contains(data, []byte("test-kernel")) {
		t.Fatal("incorrect decompressed file")
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".mihomo-install-*"))
	if len(files) != 0 {
		t.Fatal("installation temporary file retained")
	}
}

func TestRealMihomoRoutingAndListeners(t *testing.T) {
	binary := os.Getenv("SP_TEST_MIHOMO")
	if binary == "" {
		t.Skip("set SP_TEST_MIHOMO to test a real kernel")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	dns, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer dns.Close()
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, address, err := dns.ReadFrom(buffer)
			if err != nil {
				return
			}
			packet := append([]byte{}, buffer[:n]...)
			if len(packet) < 17 {
				continue
			}
			end := 12
			labels := []string{}
			for end < len(packet) && packet[end] != 0 {
				length := int(packet[end])
				end++
				if end+length > len(packet) {
					break
				}
				labels = append(labels, string(packet[end:end+length]))
				end += length
			}
			end++
			if end+4 > len(packet) {
				continue
			}
			qtype := binary16(packet[end : end+2])
			question := packet[12 : end+4]
			response := append([]byte{}, packet[:12]...)
			response[2] = 0x81
			response[3] = 0x80
			for i := 6; i < 12; i++ {
				response[i] = 0
			}
			response = append(response, question...)
			if qtype == 1 {
				response[7] = 1
				ip := []byte{127, 0, 0, 1}
				if strings.HasPrefix(strings.Join(labels, "."), "public-") {
					ip = []byte{93, 184, 216, 34}
				}
				response = append(response, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4)
				response = append(response, ip...)
			}
			dns.WriteTo(response, address)
		}
	}()
	private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "PRIVATE_OK") }))
	defer private.Close()
	privateURL, _ := url.Parse(private.URL)
	socks, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer socks.Close()
	hosts := make(chan string, 4)
	go func() {
		for {
			conn, err := socks.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				header := make([]byte, 2)
				if _, err := io.ReadFull(conn, header); err != nil {
					return
				}
				methods := make([]byte, int(header[1]))
				if _, err := io.ReadFull(conn, methods); err != nil {
					return
				}
				conn.Write([]byte{5, 0})
				request := make([]byte, 4)
				if _, err := io.ReadFull(conn, request); err != nil {
					return
				}
				var host string
				switch request[3] {
				case 3:
					length := []byte{0}
					if _, err := io.ReadFull(conn, length); err != nil {
						return
					}
					name := make([]byte, int(length[0]))
					if _, err := io.ReadFull(conn, name); err != nil {
						return
					}
					host = string(name)
				case 1:
					ip := make([]byte, 4)
					if _, err := io.ReadFull(conn, ip); err != nil {
						return
					}
					host = net.IP(ip).String()
				default:
					return
				}
				port := make([]byte, 2)
				if _, err := io.ReadFull(conn, port); err != nil {
					return
				}
				hosts <- host
				conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
				reader := bufio.NewReader(conn)
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					if line == "\r\n" {
						break
					}
				}
				io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 9\r\nConnection: close\r\n\r\nTUNNEL_OK")
			}(conn)
		}
	}()
	port := freePort(t)
	_, socksPort, _ := net.SplitHostPort(socks.Addr().String())
	subscription := strings.Replace(subscriptionFixture, "port: 9}", "port: "+socksPort+"}", 1)
	c := Config{Port: port}
	merged, err := mergeConfig([]byte(subscription), []byte("rules: ['MATCH,PROXY']\n"), c, dns.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "runtime.yaml")
	if err := atomicWrite(path, merged, 0600); err != nil {
		t.Fatal(err)
	}
	check := exec.Command(binary, "-t", "-d", directory, "-f", path)
	if data, err := check.CombinedOutput(); err != nil {
		t.Fatal(string(data), err)
	}
	log, err := os.Create(filepath.Join(directory, "log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	command := exec.Command(binary, "-d", directory, "-f", path)
	command.Stdout = log
	command.Stderr = log
	kernel, err := startChild(command)
	if err != nil {
		t.Fatal(err)
	}
	defer stopOwned(kernel)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 100*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if kernel.exited() {
			data, _ := os.ReadFile(log.Name())
			t.Fatal(string(data))
		}
		time.Sleep(30 * time.Millisecond)
	}
	proxy, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(port))
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}, Timeout: 3 * time.Second}
	for _, test := range []struct{ url, body string }{{"http://private-review.invalid:" + privateURL.Port(), "PRIVATE_OK"}, {"http://public-review.invalid:80", "TUNNEL_OK"}} {
		response, err := client.Get(test.url)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || string(data) != test.body {
			t.Fatalf("routing %s: %s (%v)", test.url, data, err)
		}
	}
	select {
	case host := <-hosts:
		if host != "public-review.invalid" {
			t.Fatal("proxy did not receive domain", host)
		}
	case <-time.After(time.Second):
		t.Fatal("proxy node was never used")
	}
	if runtime.GOOS == "linux" {
		id, err := processIdentity(kernel.cmd.Process.Pid)
		if err != nil || !ownsListener(id, port) {
			t.Fatal("kernel does not own listener", err)
		}
		// No subscription DNS/API/TUN/redirect/listener ports can survive the merge.
		for _, key := range []string{"redir-port", "mixed-port", "socks-port", "external-controller", "listeners", "tun"} {
			out, _ := parseYAML(merged)
			if _, ok := out[key]; ok {
				t.Fatal("unexpected listener configuration", key)
			}
		}
	}
}
func binary16(data []byte) uint16 { return binary.BigEndian.Uint16(data) }

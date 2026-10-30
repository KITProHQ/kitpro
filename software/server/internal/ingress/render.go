package ingress

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strconv"
)

const acmeChallengeSocket = "/run/kitpro-ingress/acme.sock"

type RenderOptions struct {
	CertificateRoot string
	WorkerUser      string
}

func RenderNginx(routes []AppliedRoute, options RenderOptions) ([]byte, error) {
	routes, err := NormalizeRoutes(routes)
	if err != nil {
		return nil, err
	}
	if options.CertificateRoot == "" {
		options.CertificateRoot = "/var/lib/kitpro-helper/ingress/certificates"
	}
	if options.WorkerUser == "" {
		options.WorkerUser = "kitpro-ingress"
	}
	if !identityPattern.MatchString(options.WorkerUser) || !filepath.IsAbs(options.CertificateRoot) || filepath.Clean(options.CertificateRoot) != options.CertificateRoot {
		return nil, fmt.Errorf("invalid nginx render options")
	}
	var out bytes.Buffer
	fmt.Fprintf(&out, "user %s;\nworker_processes auto;\npid run/nginx.pid;\nerror_log logs/error.log warn;\nevents { worker_connections 1024; }\nhttp {\n", options.WorkerUser)
	out.WriteString("  client_body_temp_path temp-client-body;\n  proxy_temp_path temp-proxy;\n  fastcgi_temp_path temp-fastcgi;\n  uwsgi_temp_path temp-uwsgi;\n  scgi_temp_path temp-scgi;\n")
	out.WriteString("  access_log logs/access.log combined;\n  server_tokens off;\n  proxy_http_version 1.1;\n  map $http_upgrade $kitpro_connection { default upgrade; '' close; }\n")
	out.WriteString("  server { listen 80 default_server; server_name _;\n")
	writeACMEChallengeLocation(&out)
	out.WriteString("    location / { return 444; }\n  }\n")
	out.WriteString("  server { listen 443 ssl default_server; ssl_reject_handshake on; server_name _; }\n")
	for _, route := range routes {
		listen := "80"
		if route.ExposureMode == ManagedHTTPS || route.TLSPolicy == TLSProvided {
			out.WriteString("  server {\n    listen 80;\n    server_name " + route.Hostname + ";\n")
			writeACMEChallengeLocation(&out)
			out.WriteString("    location / { return 308 https://$host$request_uri; }\n  }\n")
			listen = "443 ssl"
		}
		out.WriteString("  server {\n    listen " + listen + ";\n    server_name " + route.Hostname + ";\n")
		if route.TLSPolicy == TLSProvided || route.TLSPolicy == TLSACME {
			certRoot := filepath.Join(options.CertificateRoot, route.CertificateID)
			out.WriteString("    ssl_certificate " + filepath.Join(certRoot, "fullchain.pem") + ";\n")
			out.WriteString("    ssl_certificate_key " + filepath.Join(certRoot, "privkey.pem") + ";\n")
			out.WriteString("    ssl_protocols TLSv1.2 TLSv1.3;\n    ssl_session_tickets off;\n")
		}
		out.WriteString("    location / {\n")
		out.WriteString("      proxy_pass " + route.UpstreamProtocol + "://" + route.Backend.Address + ":" + strconv.Itoa(route.Backend.Port) + ";\n")
		out.WriteString("      proxy_set_header Host $host;\n      proxy_set_header X-Forwarded-Host $host;\n      proxy_set_header X-Forwarded-Proto $scheme;\n      proxy_set_header X-Forwarded-For $remote_addr;\n      proxy_set_header Upgrade $http_upgrade;\n      proxy_set_header Connection $kitpro_connection;\n")
		out.WriteString("    }\n  }\n")
	}
	out.WriteString("}\n")
	return out.Bytes(), nil
}

func writeACMEChallengeLocation(out *bytes.Buffer) {
	out.WriteString("    location ^~ /.well-known/acme-challenge/ {\n")
	out.WriteString("      proxy_pass http://unix:" + acmeChallengeSocket + ":;\n")
	out.WriteString("      proxy_set_header Host $host;\n")
	out.WriteString("      proxy_pass_request_body off;\n")
	out.WriteString("      proxy_set_header Content-Length \"\";\n")
	out.WriteString("    }\n")
}

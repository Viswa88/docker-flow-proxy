package proxy

import (
	"fmt"
	"os"
	"strings"
	"text/template"
	"bytes"
	"sort"
)

func getFrontTemplate(s Service) string {
	tmplString := `
{{- range .ServiceDest}}
    {{- if eq .ReqMode "http"}}
        {{- if ne .Port ""}}
    acl url_{{$.AclName}}{{.Port}}{{range .ServicePath}} {{if eq $.PathType ""}}path_beg{{end}}{{if ne $.PathType ""}}{{$.PathType}}{{end}} {{.}}{{end}}{{.SrcPortAcl}}
        {{- end}}
        {{- $length := len .UserAgent.Value}}{{if gt $length 0}}
    acl user_agent_{{$.AclName}}_{{.UserAgent.AclName}} hdr_sub(User-Agent) -i{{range .UserAgent.Value}} {{.}}{{end}}
        {{- end}}
    {{- end}}
{{- end}}
{{- if .ServiceDomain}}
    acl domain_{{.AclName}} {{.DomainFunction}}(host) -i{{range .ServiceDomain}} {{.}}{{end}}
{{- end}}
{{- if gt $.HttpsPort 0 }}
    acl http_{{.ServiceName}} src_port 80
    acl https_{{.ServiceName}} src_port 443
{{- end}}
{{- if $.RedirectWhenHttpProto}}
    {{- range .ServiceDest}}
        {{- if eq .ReqMode "http"}}
            {{- if ne .Port ""}}
    acl is_{{$.AclName}}_http hdr(X-Forwarded-Proto) http
    redirect scheme https if is_{{$.AclName}}_http url_{{$.AclName}}{{.Port}}{{if $.ServiceDomain}} domain_{{$.AclName}}{{end}}{{.SrcPortAclName}}
            {{- end}}
        {{- end}}
    {{- end}}
{{- end}}
{{- range .ServiceDest}}
    {{- if eq .ReqMode "http"}}{{- if ne .Port ""}}
    use_backend {{$.ServiceName}}-be{{.Port}} if url_{{$.AclName}}{{.Port}}{{if $.ServiceDomain}} domain_{{$.AclName}}{{end}}{{.SrcPortAclName}}
	    {{- if gt $.HttpsPort 0 }} http_{{$.ServiceName}}
    use_backend https-{{$.ServiceName}}-be{{.Port}} if url_{{$.AclName}}{{.Port}}{{if $.ServiceDomain}} domain_{{$.AclName}}{{end}} https_{{$.ServiceName}}
        {{- end}}
    {{- $length := len .UserAgent.Value}}{{if gt $length 0}} user_agent_{{$.AclName}}_{{.UserAgent.AclName}}{{end}}
        {{- if $.IsDefaultBackend}}
    default_backend {{$.ServiceName}}-be{{.Port}}
        {{- end}}
    {{- end}}{{- end}}
{{- end}}`
	return templateToString(tmplString, s)
}

func getFrontTemplateTcp(servicesByPort map[int]Services) string {
	tmpl := ""
	for _, services := range servicesByPort {
		sort.Sort(services)
		tmplString := `

{{$sd := (index . 0).ServiceDest}}{{$srcPort := (index $sd 0).SrcPort}}frontend tcpFE_{{$srcPort}}
    bind *:{{$srcPort}}
    mode tcp
	{{- if (index . 0).Debug}}{{$debugFormat := (index . 0).DebugFormat}}
    option tcplog
    log global
	    {{- if ne $debugFormat ""}}
    log-format {{$debugFormat}}
	    {{- end}}
	{{- end}}
    {{- range $s := .}}
        {{- range $sd := .ServiceDest}}
            {{- if $s.ServiceDomain}}
    acl domain_{{$s.AclName}} {{$s.DomainFunction}}(host) -i{{range $s.ServiceDomain}} {{.}}{{end}}
    use_backend {{$s.ServiceName}}-be{{$sd.Port}} if domain_{{$s.ServiceName}}
            {{- end}}
            {{- if not $s.ServiceDomain}}
    default_backend {{$s.ServiceName}}-be{{$sd.Port}}
            {{- end}}
        {{- end}}
    {{- end}}`
		tmpl += templateToString(tmplString, services)
	}
	return tmpl
}

// TODO: Change to private function when actions.GetTemplates is moved to the proxy package
// TODO: Create a single string for the template
// TODO: Unify HTTP and HTTPS into a single string
// TODO: Move to a file
func GetBackTemplate(sr *Service, mode string) string {
	sr.ProxyMode = strings.ToLower(mode)
	for i := range sr.ServiceDest {
		if strings.EqualFold(sr.ServiceDest[i].ReqMode, "sni") {
			sr.ServiceDest[i].ReqModeFormatted = "tcp"
		} else {
			sr.ServiceDest[i].ReqModeFormatted = sr.ServiceDest[i].ReqMode
		}
	}
	if len(getSecretOrEnvVar("USERS", "")) > 0 {
		sr.UseGlobalUsers = true
	}
	if strings.EqualFold(os.Getenv("DEBUG"), "true") {
		sr.Debug = true
	}

	// HTTP
	tmpl := `{{range .ServiceDest}}
backend {{$.ServiceName}}-be{{.Port}}
    mode {{.ReqModeFormatted}}
        {{- if ne $.ConnectionMode ""}}
    option {{$.ConnectionMode}}
        {{- end}}
        {{- if $.Debug}}
    log global
        {{- end}}`
	tmpl += getHeaders(sr)
	tmpl += `{{- if ne $.TimeoutServer ""}}
    timeout server {{$.TimeoutServer}}s
        {{- end}}
        {{- if ne $.TimeoutTunnel ""}}
    timeout tunnel {{$.TimeoutTunnel}}s
        {{- end}}
        {{- if ne $.ReqPathSearch ""}}
    http-request set-path %[path,regsub({{$.ReqPathSearch}},{{$.ReqPathReplace}})]
        {{- end}}
        {{- if or (eq $.ProxyMode "service") (eq $.ProxyMode "swarm")}}
            {{- if eq .VerifyClientSsl true}}
    acl valid_client_cert_{{$.ServiceName}}{{.Port}} ssl_c_used ssl_c_verify 0
    http-request deny unless valid_client_cert_{{$.ServiceName}}{{.Port}}
            {{- end}}
            {{- if $.HttpsOnly}}
    redirect scheme https if !{ ssl_fc }
            {{- end}}
    server {{$.ServiceName}} {{$.Host}}:{{.Port}}{{if eq $.CheckResolvers true}} check resolvers docker{{end}}{{if eq $.SslVerifyNone true}} ssl verify none{{end}}
        {{- /* TODO: It's Consul and it's deprecated. Remove it. */}}
        {{- else}}
    {{"{{"}}range $i, $e := service "{{$.FullServiceName}}" "any"{{"}}"}}
    server {{"{{$e.Node}}_{{$i}}_{{$e.Port}} {{$e.Address}}:{{$e.Port}}"}}
    {{"{{end}}"}}
        {{- end}}
        {{- if not .IgnoreAuthorization}}
            {{- if and ($.Users) (not .IgnoreAuthorization)}}
    acl {{$.ServiceName}}UsersAcl http_auth({{$.ServiceName}}Users)
    http-request auth realm {{$.ServiceName}}Realm if !{{$.ServiceName}}UsersAcl
            {{- end}}
            {{- if $.UseGlobalUsers}}
    acl defaultUsersAcl http_auth(defaultUsers)
    http-request auth realm defaultRealm if !defaultUsersAcl
            {{- end}}
            {{- if or ($.Users) ($.UseGlobalUsers)}}
    http-request del-header Authorization
            {{- end}}
        {{- end}}
    {{- end}}
    {{- if ne $.BackendExtra ""}}
    {{ $.BackendExtra }}
    {{- end}}
    {{- if gt .HttpsPort 0}}{{range .ServiceDest}}
backend https-{{$.ServiceName}}-be{{.Port}}
    mode {{.ReqModeFormatted}}
        {{- if ne $.ConnectionMode ""}}
    option {{$.ConnectionMode}}
        {{- end}}
        {{- if $.Debug}}
    log global
        {{- end}}`
	tmpl += getHeaders(sr)
	tmpl += `{{- if ne $.TimeoutServer ""}}
    timeout server {{$.TimeoutServer}}s
        {{- end}}
        {{- if ne $.TimeoutTunnel ""}}
    timeout tunnel {{$.TimeoutTunnel}}s
        {{- end}}
        {{- if ne $.ReqPathSearch ""}}
    http-request set-path %[path,regsub({{$.ReqPathSearch}},{{$.ReqPathReplace}})]
        {{- end}}
        {{- if or (eq $.ProxyMode "service") (eq $.ProxyMode "swarm")}}
            {{- if eq .VerifyClientSsl true}}
    acl valid_client_cert_{{$.ServiceName}}{{.Port}} ssl_c_used ssl_c_verify 0
    http-request deny unless valid_client_cert_{{$.ServiceName}}{{.Port}}
            {{- end}}
    server {{$.ServiceName}} {{$.Host}}:{{$.HttpsPort}}{{if eq $.CheckResolvers true}} check resolvers docker{{end}}{{if eq $.SslVerifyNone true}} ssl verify none{{end}}
        {{- /* TODO: It's Consul and it's deprecated. Remove it. */}}
        {{- else}}
    {{"{{"}}range $i, $e := service "{{$.FullServiceName}}" "any"{{"}}"}}
    server {{"{{$e.Node}}_{{$i}}_{{$e.Port}} {{$e.Address}}:{{$e.Port}}"}}
    {{"{{end}}"}}
        {{- end}}
        {{- if not .IgnoreAuthorization}}
            {{- if $.Users}}
    acl {{$.ServiceName}}UsersAcl http_auth({{$.ServiceName}}Users)
    http-request auth realm {{$.ServiceName}}Realm if !{{$.ServiceName}}UsersAcl
            {{- end}}
            {{- if $.UseGlobalUsers}}
    acl defaultUsersAcl http_auth(defaultUsers)
    http-request auth realm defaultRealm if !defaultUsersAcl
            {{- end}}
            {{- if or ($.Users) ($.UseGlobalUsers)}}
    http-request del-header Authorization
            {{- end}}
        {{- end}}
    {{- end}}
    {{- if ne $.BackendExtra ""}}
    {{ $.BackendExtra }}
    {{- end}}
{{- end}}`
	return tmpl
}

func getHeaders(sr *Service) string {
	tmpl := ""
	if sr.XForwardedProto {
		tmpl += `
    http-request add-header X-Forwarded-Proto https if { ssl_fc }`
	}
	for _, header := range sr.AddReqHeader {
		tmpl += fmt.Sprintf(`
    http-request add-header %s`,
			header,
		)
	}
	for _, header := range sr.SetReqHeader {
		tmpl += fmt.Sprintf(`
    http-request set-header %s`,
			header,
		)
	}
	for _, header := range sr.AddResHeader {
		tmpl += fmt.Sprintf(`
    http-response add-header %s`,
			header,
		)
	}
	for _, header := range sr.SetResHeader {
		tmpl += fmt.Sprintf(`
    http-response set-header %s`,
			header,
		)
	}
	for _, header := range sr.DelReqHeader {
		tmpl += fmt.Sprintf(`
    http-request del-header %s`,
			header,
		)
	}
	for _, header := range sr.DelResHeader {
		tmpl += fmt.Sprintf(`
    http-response del-header %s`,
			header,
		)
	}
	return tmpl
}

func templateToString(templateString string, data interface{}) string {
	tmpl, _ := template.New("template").Parse(templateString)
	var b bytes.Buffer
	tmpl.Execute(&b, data)
	return b.String()
}

func putDomainFunction(s *Service) {
	s.DomainFunction = "hdr"
	if s.ServiceDomainMatchAll {
		s.DomainFunction = "hdr_dom"
	} else {
		for i, domain := range s.ServiceDomain {
			if strings.HasPrefix(domain, "*") {
				s.ServiceDomain[i] = strings.Trim(domain, "*")
				s.DomainFunction = "hdr_end"
			}
		}
	}
}

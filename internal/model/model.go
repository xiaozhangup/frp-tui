package model

type Proxy struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	LocalIP    string `json:"localIP"`
	LocalPort  int    `json:"localPort"`
	RemotePort int    `json:"remotePort"`
}

type Connection struct {
	ID             string  `json:"id"`
	Revision       int64   `json:"revision"`
	Name           string  `json:"name"`
	ServerAddr     string  `json:"serverAddr"`
	ServerPort     int     `json:"serverPort"`
	Token          string  `json:"token"`
	TLS            bool    `json:"tls"`
	DesiredRunning bool    `json:"desiredRunning"`
	Proxies        []Proxy `json:"proxies"`
}

type Config struct {
	Revision    int64        `json:"revision"`
	FrpcPath    string       `json:"frpcPath"`
	Connections []Connection `json:"connections"`
}

type Status struct {
	ID      string `json:"id"`
	State   string `json:"state"` // stopped, starting, running, retrying, error
	PID     int    `json:"pid"`
	Error   string `json:"error,omitempty"`
	RetryAt string `json:"retryAt,omitempty"`
}

type Snapshot struct {
	Config   Config   `json:"config"`
	Statuses []Status `json:"statuses"`
}

type LogEntry struct {
	Seq  uint64 `json:"seq"`
	Time string `json:"time"`
	Text string `json:"text"`
}

type Logs struct {
	Entries []LogEntry `json:"entries"`
	LastSeq uint64     `json:"lastSeq"`
}

type Settings struct {
	Revision int64  `json:"revision"`
	FrpcPath string `json:"frpcPath"`
}

type Action struct {
	Revision int64 `json:"revision"`
}

type APIError struct {
	Error string `json:"error"`
}

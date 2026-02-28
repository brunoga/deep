package v5

type User struct {
	ID    int            `json:"id"`
	Name  string         `json:"full_name"`
	Info  Detail         `json:"info"`
	Roles []string       `json:"roles"`
	Score map[string]int `json:"score"`
	Bio   Text           `json:"bio"`
	age   int            // Unexported field
}

type Detail struct {
	Age     int
	Address string `json:"addr"`
}

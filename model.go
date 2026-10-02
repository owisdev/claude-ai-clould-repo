package main

type serpResponceModel struct {
	// Struct fields must start with upper case lette to be exported
	Position   float64       `json:"position"`
	Source     string        `json:"source"`
	Link       string        `json:"link"`
	Snippet    string        `json:"snippet"`
	Thumbnail  string        `json:"thumbnail"`
	Title      string        `json:"title"`
	Extensions []interface{} `json:"extensions"`
}

type serpResponceList struct {
	FinalList []serpResponceModel
}

type searchItem struct {
	Title string `json:"title"`
}

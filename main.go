package main

func main() {

	// initiate the search service
	searchServ := NewSearchService()

	server := NewAPIServer(":3002", searchServ)
	server.Run()
}

// go run .
// curl -X GET -H "Authorization: Bearer token" http://localhost:3002/api/v1/retail/netsearch
// curl -X POST -H "Authorization: Bearer token" http://localhost:3002/api/v1/retail/netsearch

/*
url: http://localhost:3002/api/v1/retail/netsearch
post
auth: Bearer Token Bearer token

body (json)

{
  "title": "sumsung S Pen"
}
*/

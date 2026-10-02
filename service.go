package main

import (
	"context"
	"fmt"
	"time"

	g "github.com/serpapi/google-search-results-golang"
)

type SearchService struct {
}

// inject the repo interface to the service constructor
func NewSearchService() SearchService {
	// no need for pointer in the service layer
	return SearchService{}
}

func workerGetUrlData(id int, jobs <-chan string, results chan<- []interface{}) {
	for job := range jobs {
		fmt.Println("worker", id, "started job", job)

		parameter := map[string]string{
			"engine": "google",
			"q":      job,
			"hl":     "en",
		}

		search := g.NewGoogleSearch(parameter, "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")

		rsp, err := search.GetJSON()
		if err != nil {
			fmt.Println(err)
			return
		}

		searchResults := rsp["organic_results"].([]interface{}) // casting to array of any type

		fmt.Println("worker", id, "finished job", job)
		results <- searchResults
	}
}

func getSearchResult(query string, urls []string) (*serpResponceList, error) {
	var t0 = time.Now()
	// Generate url slice
	var searchQuery []string
	//var itemVar serpResponceModel
	itemVar := &serpResponceModel{}
	FinalResult := &serpResponceList{}

	//var finalData = map[string]interface{}{}

	for _, v := range urls {

		//fmt.Sprintln(query + " site:" + v) // `${query} site:${v}`
		// update the value in UPPER CASE
		searchQuery = append(searchQuery, query+" site:"+v)
	}

	//fmt.Println(searchQuery)

	numWorkers := 3 // Define the number of workers in the pool
	jobs := make(chan string, len(searchQuery))
	results := make(chan []interface{}, len(searchQuery)) //*serpResponceModel

	// Start workers
	for w := 0; w < numWorkers; w++ {
		go workerGetUrlData(w, jobs, results)
	}

	// Sending jobs to the worker pool
	for _, url := range searchQuery {
		jobs <- url
	}
	close(jobs)

	for i := 0; i < len(searchQuery); i++ {
		res := <-results
		//fmt.Println(res)

		for _, item := range res {
			itemResult := item.(map[string]interface{})

			var position = 0.0
			if itemResult["position"] != nil {
				position = itemResult["position"].(float64)
			} else {
				position = 0
			}

			var thumbnail = ""
			if itemResult["thumbnail"] != nil {
				thumbnail = itemResult["thumbnail"].(string)
			} else {
				thumbnail = ""
			}

			extensions := []interface{}{}
			if itemResult["rich_snippet"] != nil {
				rich_snippet_rich := itemResult["rich_snippet"].(map[string]interface{})

				if rich_snippet_rich["top"] != nil {
					rich_snippet_rich_top := rich_snippet_rich["top"].(map[string]interface{})

					if rich_snippet_rich_top["extensions"] != nil {
						extensions = rich_snippet_rich_top["extensions"].([]interface{})
					}
				}

			}

			itemVar = &serpResponceModel{
				Position:   position,
				Source:     itemResult["source"].(string),
				Link:       itemResult["link"].(string),
				Snippet:    itemResult["snippet"].(string),
				Thumbnail:  thumbnail,
				Title:      itemResult["title"].(string),
				Extensions: extensions,
				//Extensions: extensions,
			}

			/*
				marshaled, err := json.MarshalIndent(itemVar, "", "   ")
				if err != nil {
					log.Fatalf("marshaling error: %s", err)
				}

				fmt.Println(string(marshaled))
			*/

			FinalResult = &serpResponceList{
				FinalList: append(FinalResult.FinalList, *itemVar),
			}
		}
		//
	}
	close(results)
	//fmt.Println(finalResult)

	/*
		jsonData, err := json.Marshal(FinalResult)
		if err != nil {
			fmt.Printf("could not marshal json: %s\n", err)
			return nil, err
		}

		fmt.Printf("json data: %s\n", jsonData)
	*/

	fmt.Printf("\n Processing time duration: %v", time.Since(t0))
	return FinalResult, nil

}

// getProductData implements ISearchProduct.
func (s *SearchService) getProductData(ctx context.Context, srchItem string) (*serpResponceList, error) {
	/*
		data := []serpResponceModel{
			{
				Position:  1,
				Source:    "Shein",
				Link:      "https://us.shein.com/UGREEN-4-Port-USB-C-To-USB-3-0-Hub-For-IPad-Pro-2020-Samsung-Galaxy-S22-Ultra-S21-Ultra-Slim-High-Speed-USB-Splitter-Portable-Extension-Data-Hub-Compatible-For-Book-Pro-Mini-Surface-Pro-XPS-PS4-Xbox-One-Ipad-Pro-2021-p-12335716.html",
				Snippet:   "UGREEN 4-Port USB C To USB 3.0 Hub For IPad Pro 2020 Samsung Galaxy S22 Ultra S21, Ultra Slim High-Speed USB Splitter Portable Extension Data Hub Compatible ...",
				Thumbnail: "",
				Title:     "UGREEN 4-Port USB C To USB 3.0 Hub For IPad Pro 2020 ...",
			},
			{
				Position:  2,
				Source:    "Shein",
				Link:      "https://us.shein.com/UGREEN-4-Port-USB-C-To-USB-3-0-Hub-For-IPad-Pro-2020-Samsung-Galaxy-S22-Ultra-S21-Ultra-Slim-High-Speed-USB-Splitter-Portable-Extension-Data-Hub-Compatible-For-Book-Pro-Mini-Surface-Pro-XPS-PS4-Xbox-One-Ipad-Pro-2021-p-12335716.html",
				Snippet:   "UGREEN 4-Port USB C To USB 3.0 Hub For IPad Pro 2020 Samsung Galaxy S22 Ultra S21, Ultra Slim High-Speed USB Splitter Portable Extension Data Hub Compatible ...",
				Thumbnail: "",
				Title:     "UGREEN 4-Port USB C To USB 3.0 Hub For IPad Pro 2020 ...",
			},
		}

		result := &serpResponceList{FinalList: data}
	*/
	//
	slc := []string{"amazon.com", "ebay.com", "shein.com", "aliexpress.com", "walmart.com"}
	result, err := getSearchResult(srchItem, slc) // "ugreen 4-port usb 3.0 hub"
	//

	if err != nil {
		return nil, err
	}

	return result, nil
}

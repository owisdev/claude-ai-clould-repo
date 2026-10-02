package main

import (
	"encoding/json"
	"net/http"
)

func (s *APIServer) handleGetHello(w http.ResponseWriter, r *http.Request) error {
	defer r.Body.Close()
	prodTitle := r.URL.Query().Get("prodtitle")
	if prodTitle == "" {
		return WriteJSON(w, http.StatusBadRequest, "error getting search data")
	}

	searchResult, _ := s.searchService.getProductData(r.Context(), prodTitle) // check the right passing context, error management
	return WriteJSON(w, http.StatusOK, searchResult)

}

func (s *APIServer) handleProdSearch(w http.ResponseWriter, r *http.Request) error {
	defer r.Body.Close()
	/*
		body, err := io.ReadAll(r.Body)
		if err != nil {
			//http.Error(w, err.Error(), http.StatusInternalServerError)
			return WriteJSON(w, http.StatusBadRequest, "error getting search data")
		}

		sb := string(body)
		fmt.Println(sb)
	*/

	var item searchItem
	err := json.NewDecoder(r.Body).Decode(&item)
	if err != nil {
		return WriteJSON(w, http.StatusBadRequest, "error getting search data")
	}
	//fmt.Println(item.Title)

	searchResult, _ := s.searchService.getProductData(r.Context(), item.Title) // check the right passing context, error management
	return WriteJSON(w, http.StatusOK, searchResult)
}

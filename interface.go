package main

import "context"

type ISearchProduct interface {
	getProductData(context.Context) (*serpResponceList, error)
}

// https://www.simplilearn.com/tutorials/golang-tutorial/guide-to-golang-interface

package main

// resources is every resource the API serves, each wired in a file of its
// own (items.go). make resource adds one here.
var resources = []resource{
	itemsResource,
}

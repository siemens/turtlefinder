/*
Package testimg loads container images from the test Docker engine into
container engines nested inside test containers, avoiding registry pulls (and
thus registry rate limits) from within the nested engines.
*/
package testimg

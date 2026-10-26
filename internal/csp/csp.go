// Package csp holds the Content-Security-Policy for content the dashboard's
// origin serves but did not write: project icons and every reply relayed from
// another computer.
package csp

// Untrusted lets such content render as an image or a page but never run
// script, load anything or act on the dashboard's origin.
const Untrusted = "default-src 'none'; style-src 'unsafe-inline'; img-src data:; sandbox"

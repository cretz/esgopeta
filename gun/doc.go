/*
Package gun is an implementation of the Gun distributed graph database in Go.
See https://gun.eco for more information on the Gun distributed graph database.

For common use, create a Gun instance via New, use Scoped to arrive at the
desired field, then use Fetch to get/listen to values and/or Put to write
values. A simple example is in the README at https://github.com/cretz/esgopeta.

WARNING: This is an early proof-of-concept alpha version. Many pieces are not
implemented or don't work.
*/
package gun

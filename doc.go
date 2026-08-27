/*
Package ifroute provides a toolkit for managing direct network routes.
A direct route (also called an "on-link" route) points directly to a network
interface without a gateway. This package filters out routes with gateways,
focusing only on interface-based routes.

Thread Safety:
Add/Del operations are protected by per-prefix locks, enabling concurrent
modifications to different prefixes while serializing operations on the same
prefix. This protection only applies within this package; external route
modifications may conflict.

Route Marking:
All routes created by this package are marked with protocol number 186 to
distinguish them from system-managed routes and minimize conflicts with
external modifications.

Private functions do not return any context in the error,
user friendly errors are returned by the public methods.
*/
package ifroute

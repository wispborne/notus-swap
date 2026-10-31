/**
 * The path for a call to a server. '' is this server. Calls to other servers
 * go through this one, at /notus/peer/<id>/ (internal/peers).
 */
export function api(server: string, path: string) {
  return server ? `/notus/peer/${server}${path}` : path
}

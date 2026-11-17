// Binds to HOST if set, otherwise only to loopback — a common dev-server
// default. ZEROSETUP's repair (HOST=0.0.0.0) should make it reachable.
const http = require('http')
const host = process.env.HOST || '127.0.0.1'
const port = process.env.PORT || 3000

http.createServer((req, res) => res.end('Hello from fixture-loopback-default')).listen(port, host, () => {
  console.log(`listening on ${host}:${port}`)
})

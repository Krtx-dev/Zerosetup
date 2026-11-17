// Deliberately binds only the container's loopback interface, which is
// unreachable through Docker port publishing.
const http = require('http')

http.createServer((req, res) => res.end('unreachable')).listen(3000, '127.0.0.1', () => {
  console.log('listening on 127.0.0.1:3000 only')
})

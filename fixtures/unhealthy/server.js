// Listens on all interfaces but every request fails with HTTP 500.
const http = require('http')
const port = process.env.PORT || 3000

http.createServer((req, res) => {
  res.statusCode = 500
  res.end('database not configured')
}).listen(port, () => console.log(`fixture-unhealthy listening on ${port}`))

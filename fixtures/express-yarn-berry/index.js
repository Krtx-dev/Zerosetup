const express = require('express')

const app = express()
const port = process.env.PORT || 3000

app.get('/', (req, res) => res.send('Hello from fixture-express-yarn-berry'))
app.get('/healthz', (req, res) => res.json({ ok: true }))

app.listen(port, () => console.log(`fixture-express-yarn-berry listening on ${port}`))

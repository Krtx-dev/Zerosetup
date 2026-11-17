// Uses PostgreSQL and Redis via the environment ZEROSETUP injects when the
// plan includes --with-service postgres --with-service redis.
const express = require('express')
const { Client } = require('pg')
const { createClient } = require('redis')

const app = express()
const port = process.env.PORT || 3000

app.get('/', async (req, res) => {
  const pg = new Client({ connectionString: process.env.DATABASE_URL })
  const redis = createClient({ url: process.env.REDIS_URL })
  try {
    await pg.connect()
    const { rows } = await pg.query("SELECT 'postgres-ok' AS status")
    await redis.connect()
    await redis.set('fixture', 'redis-ok')
    res.send(`Hello from fixture-express-services: ${rows[0].status} ${await redis.get('fixture')}`)
  } catch (err) {
    res.status(500).send(`service error: ${err.message}`)
  } finally {
    await pg.end().catch(() => {})
    if (redis.isOpen) await redis.quit()
  }
})

app.listen(port, () => console.log(`fixture-express-services listening on ${port}`))

from fastapi import FastAPI

app = FastAPI(title="fixture-fastapi")


@app.get("/")
def root():
    return {"message": "Hello from fixture-fastapi"}

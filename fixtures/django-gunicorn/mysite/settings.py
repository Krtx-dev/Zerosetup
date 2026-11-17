# Minimal development settings for the ZEROSETUP Django fixture.
SECRET_KEY = "fixture-only-not-secret"
DEBUG = True
ALLOWED_HOSTS = ["127.0.0.1", "localhost"]
ROOT_URLCONF = "mysite.urls"
INSTALLED_APPS = []
MIDDLEWARE = []
DATABASES = {}
USE_TZ = True

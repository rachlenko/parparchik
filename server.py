"""Parparchik — S3 file routing web service (Python reference implementation)."""

import json
import os
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.parse import parse_qs, urlparse

import boto3
from botocore.config import Config as BotoConfig


class FileRegistry:
    def __init__(self, public_bucket: str, private_bucket: str):
        self._public_bucket = public_bucket
        self._private_bucket = private_bucket
        self._entries: dict[str, dict] = {}
        self._lock = threading.Lock()

    def register(self, key: str, bucket_type: str, bucket: str, size: int):
        with self._lock:
            self._entries[key] = {
                "key": key,
                "bucket_type": bucket_type,
                "bucket": bucket,
                "route": self._make_route(key, bucket_type),
                "size": size,
            }

    def lookup(self, key: str) -> dict | None:
        with self._lock:
            return self._entries.get(key)

    def lookup_by_route(self, route: str) -> dict | None:
        with self._lock:
            for entry in self._entries.values():
                if entry["route"] == route:
                    return entry
            return None

    def list_all(self) -> list[dict]:
        with self._lock:
            return list(self._entries.values())

    def clear(self):
        with self._lock:
            self._entries.clear()

    @staticmethod
    def _make_route(key: str, bucket_type: str) -> str:
        prefix = "/public/" if bucket_type == "public" else "/private/"
        return prefix + key


class S3Backend:
    def __init__(self, endpoint: str, external_endpoint: str, region: str):
        self._endpoint = endpoint
        self._external_endpoint = external_endpoint or endpoint
        self._region = region
        kwargs = {
            "region_name": region,
            "aws_access_key_id": os.environ.get("AWS_ACCESS_KEY_ID", ""),
            "aws_secret_access_key": os.environ.get("AWS_SECRET_ACCESS_KEY", ""),
            "config": BotoConfig(signature_version="s3v4"),
        }
        if endpoint:
            kwargs["endpoint_url"] = f"http://{endpoint}"
        self._client = boto3.client("s3", **kwargs)

        self._ext_client = self._client
        if external_endpoint and external_endpoint != endpoint:
            ext_kwargs = dict(kwargs)
            ext_kwargs["endpoint_url"] = f"http://{external_endpoint}"
            self._ext_client = boto3.client("s3", **ext_kwargs)

    def list_objects(self, bucket: str) -> list[dict]:
        result = []
        paginator = self._client.get_paginator("list_objects_v2")
        for page in paginator.paginate(Bucket=bucket):
            for obj in page.get("Contents", []):
                result.append({"key": obj["Key"], "size": obj["Size"]})
        return result

    def get_object(self, bucket: str, key: str) -> bytes:
        resp = self._client.get_object(Bucket=bucket, Key=key)
        return resp["Body"].read()

    def public_url(self, bucket: str, key: str) -> str:
        if self._external_endpoint:
            return f"http://{self._external_endpoint}/{bucket}/{key}"
        return f"https://{bucket}.s3.{self._region}.amazonaws.com/{key}"

    def presigned_url(self, bucket: str, key: str, expires: int = 3600) -> str:
        return self._ext_client.generate_presigned_url(
            "get_object",
            Params={"Bucket": bucket, "Key": key},
            ExpiresIn=expires,
        )


class App:
    def __init__(self):
        self.public_bucket = os.environ["PARPARCHIK_PUBLIC_BUCKET"]
        self.private_bucket = os.environ["PARPARCHIK_PRIVATE_BUCKET"]
        endpoint = os.environ.get("S3_ENDPOINT", "")
        external_endpoint = os.environ.get("S3_EXTERNAL_ENDPOINT", "")
        region = os.environ.get("AWS_REGION", "us-east-1")
        self.s3 = S3Backend(endpoint, external_endpoint, region)
        self.registry = FileRegistry(self.public_bucket, self.private_bucket)

    def sync(self):
        self.registry.clear()
        for obj in self.s3.list_objects(self.public_bucket):
            self.registry.register(obj["key"], "public", self.public_bucket, obj["size"])
        for obj in self.s3.list_objects(self.private_bucket):
            if not self.registry.lookup(obj["key"]):
                self.registry.register(
                    obj["key"], "private", self.private_bucket, obj["size"]
                )


app: App | None = None


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        parsed = urlparse(self.path)
        path = parsed.path
        params = parse_qs(parsed.query)

        if path == "/status":
            self._handle_status()
        elif path == "/list":
            self._handle_list()
        elif path == "/update":
            self._handle_update(params)
        elif path.startswith("/public/") or path.startswith("/private/"):
            self._handle_download(path)
        else:
            self._json_response(404, {"error": "not found"})

    def _handle_status(self):
        self._json_response(200, {
            "status": "ok",
            "public_bucket": app.public_bucket,
            "private_bucket": app.private_bucket,
            "file_count": len(app.registry.list_all()),
        })

    def _handle_list(self):
        app.sync()
        entries = app.registry.list_all()
        self._json_response(200, {"count": len(entries), "files": entries})

    def _handle_update(self, params: dict):
        filenames = params.get("filename")
        if not filenames:
            self._json_response(400, {"error": "missing 'filename' query parameter"})
            return

        filename = filenames[0]
        app.sync()
        entry = app.registry.lookup(filename)
        if not entry:
            self._json_response(404, {"error": "file not found", "filename": filename})
            return

        self._json_response(200, {"message": "file located", "file": entry})

    def _handle_download(self, path: str):
        app.sync()
        entry = app.registry.lookup_by_route(path)
        if not entry:
            self._json_response(404, {"error": "file not found", "route": path})
            return

        if entry["bucket_type"] == "public":
            url = app.s3.public_url(entry["bucket"], entry["key"])
        else:
            url = app.s3.presigned_url(entry["bucket"], entry["key"])

        self.send_response(302)
        self.send_header("Location", url)
        self.end_headers()

    def _json_response(self, status: int, body: dict):
        data = json.dumps(body, indent=2).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, format, *args):
        print(f"[parparchik] {args[0]}")


def main():
    global app
    app = App()
    app.sync()

    host = os.environ.get("PARPARCHIK_HOST", "0.0.0.0")
    port = int(os.environ.get("PARPARCHIK_PORT", "8080"))

    server = HTTPServer((host, port), Handler)
    print(f"parparchik listening on {host}:{port}")
    server.serve_forever()


if __name__ == "__main__":
    main()

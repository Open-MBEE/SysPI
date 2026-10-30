#!/usr/bin/env python3
"""Serves a fake release tree for the installer tests, with GitHub's /releases/latest redirect.

    install-test-server.py <site directory> <latest tag>

Prints the port it listens on, then serves until killed. Paths under the site are served as
files; /releases/latest redirects to /releases/tag/<latest tag>, which answers empty, as
GitHub does.
"""

import functools
import http.server
import sys

site, tag = sys.argv[1], sys.argv[2]


class Handler(http.server.SimpleHTTPRequestHandler):
    def redirect(self):
        if self.path == "/releases/latest":
            self.send_response(302)
            self.send_header("Location", "/releases/tag/" + tag)
            self.end_headers()
            return True
        if self.path == "/releases/tag/" + tag:
            self.send_response(200)
            self.send_header("Content-Length", "0")
            self.end_headers()
            return True
        return False

    def do_HEAD(self):
        self.redirect() or super().do_HEAD()

    def do_GET(self):
        self.redirect() or super().do_GET()

    def log_message(self, *args):
        pass


server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), functools.partial(Handler, directory=site))
print(server.server_address[1], flush=True)
server.serve_forever()

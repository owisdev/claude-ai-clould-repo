"""Logging TCP relay for checking what an MCP client (e.g. Claude Code)
sends to the server and gets back. Listens on 127.0.0.1:3003 and passes
everything to 127.0.0.1:3002, printing the start of every request and
reply (API keys shown as ofd_live_HIDDEN). Point the client at
http://127.0.0.1:3003/mcp instead of :3002. Plain HTTP only (it cannot
look into HTTPS), so use it against a local or plain-HTTP port.

    python docs/relay_log.py
"""
import re, socket, threading
LISTEN, TARGET = ("127.0.0.1", 3003), ("127.0.0.1", 3002)
lock = threading.Lock()

def pipe(src, dst, label):
    try:
        while True:
            data = src.recv(65536)
            if not data:
                break
            with lock:
                print(f"--- {label} ({len(data)} bytes):", flush=True)
                print(repr(re.sub(rb'ofd_live_[a-z2-7]+', b'ofd_live_HIDDEN', data[:400])), flush=True)
            dst.sendall(data)
    except OSError as e:
        with lock:
            print(f"--- {label}: {e}")
    finally:
        try:
            dst.shutdown(socket.SHUT_WR)
        except OSError:
            pass

def handle(client):
    upstream = socket.create_connection(TARGET)
    threading.Thread(target=pipe, args=(client, upstream, "Claude Code -> server"), daemon=True).start()
    pipe(upstream, client, "server -> Claude Code")
    client.close()
    upstream.close()

server = socket.socket()
server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
server.bind(LISTEN)
server.listen(50)
print("relay 127.0.0.1:3003 -> 127.0.0.1:3002, logging (Ctrl+C to stop)", flush=True)
while True:
    conn, _ = server.accept()
    threading.Thread(target=handle, args=(conn,), daemon=True).start()

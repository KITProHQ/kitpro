import json, socket, struct, sys, time
for i, (op, params) in enumerate((("Ping", {}), ("InspectDocker", {}), ("PrepareTestDirectory", {"slot": "go-slot"}), ("Openat2Validation", {}), ("NegativeProbe", {}))):
    s = socket.socket(socket.AF_UNIX)
    s.connect(sys.argv[1])
    body = json.dumps({"version": 1, "request_id": f"go-{int(time.time())}-{i}", "operation": op, "params": params}).encode()
    s.sendall(struct.pack(">I", len(body)) + body)
    n = struct.unpack(">I", s.recv(4))[0]
    print(s.recv(n).decode())
    s.close()

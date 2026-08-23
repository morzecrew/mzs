// A Language Server Protocol client in plain CommonJS: spawn, frame, request, notify.
//
// The usual answer is `vscode-languageclient` from npm. This extension has no
// dependencies and no build step — drop the folder into ~/.vscode/extensions and it runs
// — and the protocol between it and `mzs lsp` is a `Content-Length` header, a blank line
// and a JSON object. That is what is implemented here, and it is the same decision the
// server side made for the same reason (SPEC §15.3).

const cp = require('child_process');

const HEADER_END = '\r\n\r\n';

class Client {
  /**
   * @param {string} command the binary to run
   * @param {string[]} args its arguments
   * @param {(method: string, params: any) => void} onNotify called for every server notification
   * @param {(message: string) => void} onError called once when the server cannot be run or dies
   */
  constructor(command, args, onNotify, onError) {
    this.command = command;
    this.args = args;
    this.onNotify = onNotify;
    this.onError = onError;
    this.proc = null;
    this.buffer = Buffer.alloc(0);
    this.nextId = 1;
    this.pending = new Map();
    this.stopped = false;
    // Nothing but `initialize` may be written before the server has answered it, so
    // everything else queues here until it has. Without the gate a `didOpen` fired by the
    // editor during activation races the handshake.
    this.initialized = false;
    this.queue = [];
  }

  start() {
    this.proc = cp.spawn(this.command, this.args, { stdio: ['pipe', 'pipe', 'pipe'] });
    this.proc.on('error', err => this.fail(`could not start "${this.command}": ${err.message}`));
    this.proc.on('exit', code => {
      if (!this.stopped) this.fail(`the language server exited with code ${code}`);
    });
    this.proc.stdout.on('data', chunk => this.receive(chunk));
    // The server writes nothing to stderr in normal operation; when it does, it is worth
    // seeing rather than swallowing.
    this.proc.stderr.on('data', chunk => this.fail(String(chunk).trim()));
    this.proc.stdin.on('error', () => {});
    return this;
  }

  fail(message) {
    if (this.stopped) return;
    this.stopped = true;
    for (const { reject } of this.pending.values()) reject(new Error(message));
    this.pending.clear();
    if (this.onError) this.onError(message);
  }

  stop() {
    if (this.stopped || !this.proc) return;
    try {
      // `exit` first: `send` refuses to write once `stopped` is set, so setting the flag
      // before the notification would drop the very message this method is here to send.
      this.write({ jsonrpc: '2.0', method: 'exit' });
      this.proc.stdin.end();
    } catch (_) {
      // The process is already gone, which is the state we were asking for.
    }
    this.stopped = true;
    this.queue = [];
    this.proc.kill();
  }

  /**
   * initialize performs the handshake and releases everything queued behind it. Every
   * other request and notification waits for the server's answer, which is what the
   * protocol asks of a client.
   */
  initialize(params) {
    const id = this.nextId++;
    const answer = new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      this.write({ jsonrpc: '2.0', id, method: 'initialize', params });
    });
    return answer.then(result => {
      this.write({ jsonrpc: '2.0', method: 'initialized', params: {} });
      this.initialized = true;
      const queued = this.queue;
      this.queue = [];
      for (const message of queued) this.write(message);
      return result;
    });
  }

  send(message) {
    if (this.stopped || !this.proc) return;
    if (!this.initialized) {
      this.queue.push(message);
      return;
    }
    this.write(message);
  }

  // write puts a message on the wire with no regard for the handshake. Only initialize,
  // the queue it releases, and `exit` use it directly.
  write(message) {
    if (!this.proc) return;
    const body = Buffer.from(JSON.stringify(message), 'utf8');
    this.proc.stdin.write(`Content-Length: ${body.length}${HEADER_END}`);
    this.proc.stdin.write(body);
  }

  notify(method, params) {
    this.send({ jsonrpc: '2.0', method, params });
  }

  request(method, params) {
    if (this.stopped) return Promise.reject(new Error('the language server is not running'));
    const id = this.nextId++;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      this.send({ jsonrpc: '2.0', id, method, params });
    });
  }

  // receive reassembles the stream. A chunk may hold half a message or three of them, so
  // the length in the header is the only thing that says where one ends.
  receive(chunk) {
    this.buffer = Buffer.concat([this.buffer, chunk]);
    for (;;) {
      const headerEnd = this.buffer.indexOf(HEADER_END);
      if (headerEnd < 0) return;
      const header = this.buffer.slice(0, headerEnd).toString('ascii');
      const match = /content-length:\s*(\d+)/i.exec(header);
      if (!match) {
        // Unreadable framing: there is no way to find the next message, so give up
        // rather than resynchronise on a guess.
        this.fail('the language server sent a message with no Content-Length');
        return;
      }
      const length = Number(match[1]);
      const start = headerEnd + HEADER_END.length;
      if (this.buffer.length < start + length) return;
      const body = this.buffer.slice(start, start + length).toString('utf8');
      this.buffer = this.buffer.slice(start + length);
      this.dispatch(body);
    }
  }

  dispatch(body) {
    let message;
    try {
      message = JSON.parse(body);
    } catch (err) {
      this.fail(`the language server sent something that is not JSON: ${err.message}`);
      return;
    }
    if (message.id !== undefined && this.pending.has(message.id)) {
      const { resolve, reject } = this.pending.get(message.id);
      this.pending.delete(message.id);
      if (message.error) reject(new Error(message.error.message || 'request failed'));
      else resolve(message.result);
      return;
    }
    if (message.method && message.id === undefined) {
      this.onNotify(message.method, message.params);
      return;
    }
    if (message.method && message.id !== undefined) {
      // The server asks nothing of the client today. Answering "no such method" keeps a
      // future server from waiting on a reply that never comes.
      this.send({
        jsonrpc: '2.0',
        id: message.id,
        error: { code: -32601, message: 'this client implements no requests' },
      });
    }
  }
}

module.exports = { Client };

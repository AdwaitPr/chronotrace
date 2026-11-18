/// <reference lib="webworker" />
import type { PointInTimeState } from '../types';

let ws: WebSocket | null = null;
let wsUrl = 'ws://localhost:8080/ws/scrub';
let reconnectTimer: any = null;
let isConnected = false;
let pendingScrubMs: number | null = null;
let throttleTimer: any = null;

const MSG_TYPE_SCRUB_REQ = 0x01;
const MSG_TYPE_STATE_FRAME = 0x02;

function connect() {
	if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
		return;
	}

	try {
		ws = new WebSocket(wsUrl);
		ws.binaryType = 'arraybuffer';

		ws.onopen = () => {
			isConnected = true;
			self.postMessage({ type: 'STATUS', connected: true });
			if (pendingScrubMs !== null) {
				sendScrubFrame(pendingScrubMs);
			}
		};

		ws.onclose = () => {
			isConnected = false;
			self.postMessage({ type: 'STATUS', connected: false });
			scheduleReconnect();
		};

		ws.onerror = () => {
			isConnected = false;
			self.postMessage({ type: 'STATUS', connected: false });
		};

		ws.onmessage = (event: MessageEvent) => {
			if (event.data instanceof ArrayBuffer) {
				handleBinaryFrame(event.data);
			} else if (typeof event.data === 'string') {
				try {
					const data = JSON.parse(event.data);
					self.postMessage({ type: 'STATE_FRAME', timestampMs: Date.now(), state: data });
				} catch {
					// Ignore invalid JSON
				}
			}
		};
	} catch {
		scheduleReconnect();
	}
}

function scheduleReconnect() {
	if (reconnectTimer) clearTimeout(reconnectTimer);
	reconnectTimer = setTimeout(() => {
		connect();
	}, 2000);
}

function handleBinaryFrame(buffer: ArrayBuffer) {
	if (buffer.byteLength < 13) return;

	const view = new DataView(buffer);
	const msgType = view.getUint8(0);

	if (msgType !== MSG_TYPE_STATE_FRAME) return;

	// Read big-endian 64-bit int timestamp_ms
	const timestampMs = Number(view.getBigInt64(1, false));
	const payloadLen = view.getUint32(9, false);

	if (buffer.byteLength < 13 + payloadLen) return;

	const payloadBytes = new Uint8Array(buffer, 13, payloadLen);
	const decoder = new TextDecoder('utf-8');
	const jsonStr = decoder.decode(payloadBytes);

	try {
		const state = JSON.parse(jsonStr) as PointInTimeState;
		self.postMessage({
			type: 'STATE_FRAME',
			timestampMs,
			state
		});
	} catch (e) {
		console.error('[Worker] Failed to parse state payload:', e);
	}
}

function sendScrubFrame(timestampMs: number) {
	if (!ws || ws.readyState !== WebSocket.OPEN) {
		pendingScrubMs = timestampMs;
		return;
	}

	const buffer = new ArrayBuffer(9);
	const view = new DataView(buffer);
	view.setUint8(0, MSG_TYPE_SCRUB_REQ);
	view.setBigInt64(1, BigInt(Math.floor(timestampMs)), false);

	try {
		ws.send(buffer);
		pendingScrubMs = null;
	} catch (e) {
		console.error('[Worker] Failed to send scrub frame:', e);
	}
}

function queueScrub(timestampMs: number) {
	pendingScrubMs = timestampMs;
	if (!throttleTimer) {
		throttleTimer = setTimeout(() => {
			throttleTimer = null;
			if (pendingScrubMs !== null) {
				sendScrubFrame(pendingScrubMs);
			}
		}, 16); // 60 FPS throttled outbound requests
	}
}

self.onmessage = (e: MessageEvent) => {
	const msg = e.data;
	switch (msg.type) {
		case 'INIT':
			if (msg.wsUrl) wsUrl = msg.wsUrl;
			connect();
			break;
		case 'SCRUB':
			queueScrub(msg.timestampMs);
			break;
		case 'DISCONNECT':
			if (ws) ws.close();
			break;
	}
};

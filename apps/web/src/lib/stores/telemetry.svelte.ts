import type {
	PointInTimeState,
	TimelineOverviewResponse,
	DeployEvent,
	WaveformBucket,
	IncidentHighlight,
	IncidentNote,
	SavedBookmark
} from '../types';

export class TelemetryStudioState {
	// Connection & Loading
	isConnected = $state(false);
	isLoading = $state(true);
	apiBaseUrl = $state('http://localhost:8080');
	wsUrl = $state('ws://localhost:8080/ws/scrub');

	// Timeline Viewport & Overview
	fromMs = $state(Date.now() - 15 * 60 * 1000);
	toMs = $state(Date.now());
	buckets = $state<WaveformBucket[]>([]);
	deploys = $state<DeployEvent[]>([]);
	highlights = $state<IncidentHighlight[]>([]);

	// Playhead & Shuttle
	playheadMs = $state(Date.now());
	isPlaying = $state(false);
	shuttleSpeed = $state(1); // -8x to +8x
	loopInMs = $state<number | null>(null);
	loopOutMs = $state<number | null>(null);
	isLoopEnabled = $state(false);

	// Reconstructed State at Playhead
	currentState = $state<PointInTimeState | null>(null);

	// Filtering & Cockpit
	selectedService = $state<string | null>(null);
	activeScenario = $state('canary-bad-pool');

	// Postmortem Notes & Bookmarks
	notes = $state<IncidentNote[]>([]);
	bookmarks = $state<SavedBookmark[]>([]);

	// Internal worker & animation
	private worker: Worker | null = null;
	private rafId: number | null = null;
	private lastRafTime: number | null = null;

	// Derived statistics
	totalThroughput = $derived(this.currentState?.total_throughput ?? 0);
	overallErrorRate = $derived(this.currentState?.overall_error_rate ?? 0);
	topSuspect = $derived(this.currentState?.suspects?.[0] ?? null);
	activeNodes = $derived(this.currentState?.nodes ?? []);
	activeEdges = $derived(this.currentState?.edges ?? []);

	constructor() {
		// Browser environment check
		if (typeof window !== 'undefined') {
			this.initWorker();
			this.fetchOverview();
			this.fetchNotes();
			this.fetchBookmarks();
			this.startPlaybackLoop();
		}
	}

	private initWorker() {
		try {
			this.worker = new Worker(new URL('../workers/decoder.worker.ts', import.meta.url), {
				type: 'module'
			});

			this.worker.onmessage = (e: MessageEvent) => {
				const msg = e.data;
				if (msg.type === 'STATUS') {
					this.isConnected = msg.connected;
				} else if (msg.type === 'STATE_FRAME') {
					this.currentState = msg.state;
				}
			};

			this.worker.postMessage({
				type: 'INIT',
				wsUrl: this.wsUrl
			});
		} catch (err) {
			console.error('Failed to initialize decoder worker:', err);
		}
	}

	async fetchOverview() {
		this.isLoading = true;
		try {
			const res = await fetch(
				`${this.apiBaseUrl}/api/timeline/overview?from=${this.fromMs}&to=${this.toMs}&buckets=300`
			);
			if (res.ok) {
				const data = (await res.json()) as TimelineOverviewResponse;
				this.buckets = data.buckets || [];
				this.deploys = data.deploys || [];
				this.highlights = data.highlights || [];
				if (data.from_ms && data.to_ms) {
					this.fromMs = data.from_ms;
					this.toMs = data.to_ms;
				}
				// If playhead out of range, clamp
				if (this.playheadMs < this.fromMs || this.playheadMs > this.toMs) {
					this.seekTo(this.toMs);
				}
			}
		} catch (e) {
			console.error('Failed to fetch timeline overview:', e);
		} finally {
			this.isLoading = false;
		}
	}

	async fetchNotes() {
		try {
			const res = await fetch(`${this.apiBaseUrl}/api/notes?incident_id=default`);
			if (res.ok) {
				this.notes = await res.json();
			}
		} catch (e) {
			console.error('Failed to fetch notes:', e);
		}
	}

	async addNote(title: string, content: string, author: string = 'oncall-sre') {
		try {
			const res = await fetch(`${this.apiBaseUrl}/api/notes`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					incident_id: 'default',
					title,
					author,
					content,
					created_at: Date.now() * 1e6
				})
			});
			if (res.ok) {
				const note = await res.json();
				this.notes = [note, ...this.notes];
			}
		} catch (e) {
			console.error('Failed to add note:', e);
		}
	}

	async fetchBookmarks() {
		try {
			const res = await fetch(`${this.apiBaseUrl}/api/bookmarks`);
			if (res.ok) {
				this.bookmarks = await res.json();
			}
		} catch (e) {
			console.error('Failed to fetch bookmarks:', e);
		}
	}

	async saveBookmark(name: string, description: string = '') {
		try {
			const id = 'bm-' + Date.now();
			const res = await fetch(`${this.apiBaseUrl}/api/bookmarks`, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify({
					id,
					name,
					timestamp_unix_nano: this.playheadMs * 1e6,
					window_seconds: 30,
					description,
					created_at: Date.now() * 1e6
				})
			});
			if (res.ok) {
				const bm = await res.json();
				this.bookmarks = [bm, ...this.bookmarks];
			}
		} catch (e) {
			console.error('Failed to save bookmark:', e);
		}
	}

	seekTo(timestampMs: number) {
		const clamped = Math.max(this.fromMs, Math.min(this.toMs, timestampMs));
		this.playheadMs = clamped;
		this.dispatchScrub(clamped);
	}

	private dispatchScrub(timestampMs: number) {
		if (this.worker) {
			this.worker.postMessage({
				type: 'SCRUB',
				timestampMs
			});
		}
	}

	// J/K/L Shuttle Playback Controls
	shuttleRewind() {
		if (!this.isPlaying) {
			this.isPlaying = true;
			this.shuttleSpeed = -1;
		} else if (this.shuttleSpeed > 0) {
			this.shuttleSpeed = -1;
		} else if (this.shuttleSpeed === -1) {
			this.shuttleSpeed = -2;
		} else if (this.shuttleSpeed === -2) {
			this.shuttleSpeed = -4;
		} else if (this.shuttleSpeed === -4) {
			this.shuttleSpeed = -8;
		}
	}

	togglePlayPause() {
		this.isPlaying = !this.isPlaying;
		if (this.isPlaying) {
			this.shuttleSpeed = 1;
		}
	}

	shuttleForward() {
		if (!this.isPlaying) {
			this.isPlaying = true;
			this.shuttleSpeed = 1;
		} else if (this.shuttleSpeed < 0) {
			this.shuttleSpeed = 1;
		} else if (this.shuttleSpeed === 1) {
			this.shuttleSpeed = 2;
		} else if (this.shuttleSpeed === 2) {
			this.shuttleSpeed = 4;
		} else if (this.shuttleSpeed === 4) {
			this.shuttleSpeed = 8;
		}
	}

	stepFrame(forward: boolean = true) {
		this.isPlaying = false;
		const stepMs = 500; // 500ms frame step
		this.seekTo(this.playheadMs + (forward ? stepMs : -stepMs));
	}

	setLoopIn() {
		this.loopInMs = this.playheadMs;
		if (this.loopOutMs !== null && this.loopInMs >= this.loopOutMs) {
			this.loopOutMs = this.loopInMs + 10000;
		}
		this.isLoopEnabled = true;
	}

	setLoopOut() {
		this.loopOutMs = this.playheadMs;
		if (this.loopInMs !== null && this.loopOutMs <= this.loopInMs) {
			this.loopInMs = this.loopOutMs - 10000;
		}
		this.isLoopEnabled = true;
	}

	clearLoop() {
		this.loopInMs = null;
		this.loopOutMs = null;
		this.isLoopEnabled = false;
	}

	isolateService(name: string | null) {
		if (this.selectedService === name) {
			this.selectedService = null;
		} else {
			this.selectedService = name;
		}
	}

	private startPlaybackLoop() {
		const loop = (now: number) => {
			if (this.lastRafTime === null) {
				this.lastRafTime = now;
			}
			const deltaSec = (now - this.lastRafTime) / 1000;
			this.lastRafTime = now;

			if (this.isPlaying) {
				let nextMs = this.playheadMs + deltaSec * 1000 * this.shuttleSpeed;

				// Loop boundary check
				if (
					this.isLoopEnabled &&
					this.loopInMs !== null &&
					this.loopOutMs !== null &&
					this.loopOutMs > this.loopInMs
				) {
					if (nextMs >= this.loopOutMs) {
						nextMs = this.loopInMs;
					} else if (nextMs <= this.loopInMs) {
						nextMs = this.loopOutMs;
					}
				} else {
					// Clamp to overview bounds
					if (nextMs >= this.toMs) {
						nextMs = this.toMs;
						this.isPlaying = false;
					} else if (nextMs <= this.fromMs) {
						nextMs = this.fromMs;
						this.isPlaying = false;
					}
				}

				this.playheadMs = nextMs;
				this.dispatchScrub(nextMs);
			}

			this.rafId = requestAnimationFrame(loop);
		};

		this.rafId = requestAnimationFrame(loop);
	}

	destroy() {
		if (this.rafId) cancelAnimationFrame(this.rafId);
		if (this.worker) this.worker.terminate();
	}
}

export const studio = new TelemetryStudioState();

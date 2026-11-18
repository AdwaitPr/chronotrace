<script lang="ts">
	import { onMount } from 'svelte';
	import { studio } from '../stores/telemetry.svelte';
	import type { DeployEvent, IncidentHighlight, WaveformBucket } from '../types';

	let canvas: HTMLCanvasElement;
	let container: HTMLDivElement;
	let width = $state(1200);
	const height = 260; // 4 tracks height

	let isDraggingPlayhead = false;
	let hoveredDeploy = $state<DeployEvent | null>(null);
	let tooltipPos = $state<{ x: number; y: number } | null>(null);

	const TRACK_HEIGHTS = [45, 90, 80, 45]; // Track 1: Deploys, Track 2: Waveforms, Track 3: Swimlanes, Track 4: Bookmarks/Loop
	const TRACK_Y = [0, 45, 135, 215];

	const SERVICES = [
		'edge-cdn',
		'api-gateway',
		'auth-svc',
		'catalog-svc',
		'cart-svc',
		'checkout-svc',
		'fraud-engine',
		'inventory-svc',
		'notification-svc',
		'payment-worker',
		'redis-cache',
		'postgres-primary'
	];

	function timeToX(ms: number): number {
		const span = studio.toMs - studio.fromMs;
		if (span <= 0) return 0;
		return ((ms - studio.fromMs) / span) * width;
	}

	function xToTime(x: number): number {
		const span = studio.toMs - studio.fromMs;
		return studio.fromMs + (x / width) * span;
	}

	onMount(() => {
		const ctx = canvas.getContext('2d');
		if (!ctx) return;

		const resize = () => {
			if (container) {
				width = container.clientWidth;
				const dpr = window.devicePixelRatio || 1;
				canvas.width = width * dpr;
				canvas.height = height * dpr;
				ctx.scale(dpr, dpr);
			}
		};
		resize();

		const observer = new ResizeObserver(resize);
		if (container) observer.observe(container);

		let animId: number;
		const render = () => {
			drawTimeline(ctx);
			animId = requestAnimationFrame(render);
		};
		animId = requestAnimationFrame(render);

		// Global Hotkey shuttle bindings
		const handleKeyDown = (e: KeyboardEvent) => {
			// Ignore if focus is in an input or textarea
			const tag = (e.target as HTMLElement)?.tagName;
			if (tag === 'INPUT' || tag === 'TEXTAREA') return;

			switch (e.key) {
				case 'j':
				case 'J':
					studio.shuttleRewind();
					break;
				case 'k':
				case 'K':
				case ' ':
					e.preventDefault();
					studio.togglePlayPause();
					break;
				case 'l':
				case 'L':
					studio.shuttleForward();
					break;
				case 'ArrowLeft':
					e.preventDefault();
					studio.stepFrame(false);
					break;
				case 'ArrowRight':
					e.preventDefault();
					studio.stepFrame(true);
					break;
				case 'i':
				case 'I':
					studio.setLoopIn();
					break;
				case 'o':
				case 'O':
					studio.setLoopOut();
					break;
				case 'Escape':
					studio.clearLoop();
					break;
			}
		};
		window.addEventListener('keydown', handleKeyDown);

		return () => {
			cancelAnimationFrame(animId);
			observer.disconnect();
			window.removeEventListener('keydown', handleKeyDown);
		};
	});

	function drawTimeline(ctx: CanvasRenderingContext2D) {
		ctx.clearRect(0, 0, width, height);

		// Background & Track separators
		ctx.fillStyle = '#090B10';
		ctx.fillRect(0, 0, width, height);

		ctx.strokeStyle = 'rgba(255, 255, 255, 0.08)';
		ctx.lineWidth = 1;
		for (let i = 1; i < TRACK_Y.length; i++) {
			ctx.beginPath();
			ctx.moveTo(0, TRACK_Y[i]);
			ctx.lineTo(width, TRACK_Y[i]);
			ctx.stroke();
		}

		// Track Labels
		ctx.font = '600 9px "JetBrains Mono", monospace';
		ctx.fillStyle = 'rgba(255, 255, 255, 0.3)';
		ctx.fillText('TRACK 1 • DEPLOY PINS', 12, 14);
		ctx.fillText('TRACK 2 • LATENCY & 5XX WAVEFORM', 12, TRACK_Y[1] + 14);
		ctx.fillText('TRACK 3 • SERVICE HEALTH SWIMLANES', 12, TRACK_Y[2] + 14);
		ctx.fillText('TRACK 4 • POSTMORTEM BOOKMARKS & I/O LOOP', 12, TRACK_Y[3] + 14);

		// 1. Draw Incident Highlight Ranges
		for (const h of studio.highlights) {
			const x1 = Math.max(0, timeToX(h.start_ms));
			const x2 = Math.min(width, timeToX(h.end_ms));
			if (x2 > x1) {
				const grad = ctx.createLinearGradient(0, 0, 0, height);
				grad.addColorStop(0, 'rgba(239, 68, 68, 0.15)');
				grad.addColorStop(1, 'rgba(239, 68, 68, 0.02)');
				ctx.fillStyle = grad;
				ctx.fillRect(x1, 0, x2 - x1, height);

				ctx.strokeStyle = 'rgba(239, 68, 68, 0.4)';
				ctx.strokeRect(x1, 0, x2 - x1, height);
			}
		}

		// 2. Track 1: Deploy & Config Pins
		for (const d of studio.deploys) {
			const dMs = d.timestamp_unix_nano / 1e6;
			const x = timeToX(dMs);
			if (x < 0 || x > width) continue;

			ctx.save();
			ctx.strokeStyle = '#38BDF8';
			ctx.lineWidth = 1.5;
			ctx.beginPath();
			ctx.moveTo(x, 18);
			ctx.lineTo(x, 42);
			ctx.stroke();

			// Pin Head Flag
			ctx.fillStyle = '#0284C7';
			ctx.beginPath();
			ctx.roundRect(x - 4, 18, 8, 12, 2);
			ctx.fill();

			// Commit chip label
			const sha = d.commit_hash.slice(0, 7);
			ctx.font = '700 8px "JetBrains Mono", monospace';
			ctx.fillStyle = '#E0F2FE';
			ctx.fillText(sha, x + 6, 28);
			ctx.restore();
		}

		// 3. Track 2: Latency & 5xx Waveform
		const buckets = studio.buckets;
		if (buckets.length > 1) {
			const t2Y = TRACK_Y[1];
			const t2H = TRACK_HEIGHTS[1];
			const maxLatency = 5000.0; // scale up to 5000ms

			// Draw 5xx Error Histogram Bars (Crimson)
			ctx.fillStyle = 'rgba(239, 68, 68, 0.45)';
			for (let i = 0; i < buckets.length; i++) {
				const b = buckets[i];
				if (b.error_rate > 0) {
					const x = timeToX(b.timestamp_ms);
					const bw = Math.max(1.5, width / buckets.length);
					const barH = Math.min(t2H - 20, b.error_rate * (t2H - 20));
					ctx.fillRect(x, t2Y + t2H - barH - 4, bw, barH);
				}
			}

			// Draw P95 and P99 Latency Curves
			ctx.lineWidth = 1.5;

			// P95 curve (amber)
			ctx.beginPath();
			ctx.strokeStyle = '#F59E0B';
			for (let i = 0; i < buckets.length; i++) {
				const b = buckets[i];
				const x = timeToX(b.timestamp_ms);
				const normalizedLat = Math.min(1.0, b.p95_ms / maxLatency);
				const y = t2Y + t2H - 4 - normalizedLat * (t2H - 25);
				if (i === 0) ctx.moveTo(x, y);
				else ctx.lineTo(x, y);
			}
			ctx.stroke();

			// P99 curve (rose/red)
			ctx.beginPath();
			ctx.strokeStyle = '#EF4444';
			ctx.lineWidth = 2.0;
			for (let i = 0; i < buckets.length; i++) {
				const b = buckets[i];
				const x = timeToX(b.timestamp_ms);
				const normalizedLat = Math.min(1.0, b.p99_ms / maxLatency);
				const y = t2Y + t2H - 4 - normalizedLat * (t2H - 25);
				if (i === 0) ctx.moveTo(x, y);
				else ctx.lineTo(x, y);
			}
			ctx.stroke();
		}

		// 4. Track 3: Service Health Swimlanes (12 microservices)
		const t3Y = TRACK_Y[2];
		const t3H = TRACK_HEIGHTS[2];
		const rowH = (t3H - 18) / SERVICES.length;

		SERVICES.forEach((svc, idx) => {
			const y = t3Y + 16 + idx * rowH;
			ctx.fillStyle = 'rgba(255, 255, 255, 0.02)';
			ctx.fillRect(0, y, width, rowH - 1);

			// Service mini tag
			ctx.font = '500 7px "JetBrains Mono", monospace';
			ctx.fillStyle = svc === studio.topSuspect?.service_name ? '#EF4444' : 'rgba(255, 255, 255, 0.4)';
			ctx.fillText(svc, 6, y + rowH - 2);

			// Status strip across buckets
			const bw = Math.max(1, width / (buckets.length || 1));
			for (let bIdx = 0; bIdx < buckets.length; bIdx++) {
				const b = buckets[bIdx];
				const bx = timeToX(b.timestamp_ms);
				// If suspect culprit during error window, paint crimson
				if (svc === studio.topSuspect?.service_name && b.error_rate > 0.05) {
					ctx.fillStyle = '#EF4444';
					ctx.fillRect(bx, y, bw, rowH - 1);
				} else if (b.error_rate > 0.05) {
					ctx.fillStyle = 'rgba(245, 158, 11, 0.4)';
					ctx.fillRect(bx, y, bw, rowH - 1);
				} else {
					ctx.fillStyle = 'rgba(16, 185, 129, 0.15)';
					ctx.fillRect(bx, y, bw, rowH - 1);
				}
			}
		});

		// 5. Track 4: Bookmarks & I/O Loop Region
		const t4Y = TRACK_Y[3];
		const t4H = TRACK_HEIGHTS[3];

		// I/O Loop Brackets
		if (studio.isLoopEnabled && studio.loopInMs !== null && studio.loopOutMs !== null) {
			const xIn = timeToX(studio.loopInMs);
			const xOut = timeToX(studio.loopOutMs);
			if (xOut > xIn) {
				ctx.fillStyle = 'rgba(6, 182, 212, 0.12)';
				ctx.fillRect(xIn, 0, xOut - xIn, height);

				ctx.strokeStyle = '#06B6D4';
				ctx.lineWidth = 2;
				// In Bracket [
				ctx.beginPath();
				ctx.moveTo(xIn + 6, 0);
				ctx.lineTo(xIn, 0);
				ctx.lineTo(xIn, height);
				ctx.lineTo(xIn + 6, height);
				ctx.stroke();

				// Out Bracket ]
				ctx.beginPath();
				ctx.moveTo(xOut - 6, 0);
				ctx.lineTo(xOut, 0);
				ctx.lineTo(xOut, height);
				ctx.lineTo(xOut - 6, height);
				ctx.stroke();
			}
		}

		// Bookmarks
		for (const bm of studio.bookmarks) {
			const bmMs = bm.timestamp_unix_nano / 1e6;
			const bx = timeToX(bmMs);
			ctx.strokeStyle = '#A855F7';
			ctx.lineWidth = 1.5;
			ctx.beginPath();
			ctx.moveTo(bx, t4Y);
			ctx.lineTo(bx, t4Y + t4H);
			ctx.stroke();

			ctx.fillStyle = '#A855F7';
			ctx.beginPath();
			ctx.arc(bx, t4Y + 10, 4, 0, Math.PI * 2);
			ctx.fill();
		}

		// 6. DRAW PLAYHEAD (Active Needle)
		const playheadX = timeToX(studio.playheadMs);
		ctx.save();
		ctx.strokeStyle = '#38BDF8';
		ctx.lineWidth = 2;
		ctx.shadowColor = '#0284C7';
		ctx.shadowBlur = 8;
		ctx.beginPath();
		ctx.moveTo(playheadX, 0);
		ctx.lineTo(playheadX, height);
		ctx.stroke();

		// Playhead top scrubbing badge
		ctx.fillStyle = '#0284C7';
		ctx.beginPath();
		ctx.moveTo(playheadX - 6, 0);
		ctx.lineTo(playheadX + 6, 0);
		ctx.lineTo(playheadX + 6, 8);
		ctx.lineTo(playheadX, 14);
		ctx.lineTo(playheadX - 6, 8);
		ctx.closePath();
		ctx.fill();
		ctx.restore();
	}

	function handleMouseDown(e: MouseEvent) {
		const rect = canvas.getBoundingClientRect();
		const x = e.clientX - rect.left;
		isDraggingPlayhead = true;
		scrubToX(x);
	}

	function handleMouseMove(e: MouseEvent) {
		const rect = canvas.getBoundingClientRect();
		const x = e.clientX - rect.left;
		const y = e.clientY - rect.top;

		if (isDraggingPlayhead) {
			scrubToX(x);
		}

		// Check hover on deploy pins in Track 1
		if (y < 45) {
			let found: DeployEvent | null = null;
			for (const d of studio.deploys) {
				const dx = timeToX(d.timestamp_unix_nano / 1e6);
				if (Math.abs(x - dx) <= 12) {
					found = d;
					break;
				}
			}
			hoveredDeploy = found;
			tooltipPos = found ? { x: e.clientX, y: e.clientY } : null;
		} else {
			hoveredDeploy = null;
		}
	}

	function handleMouseUp() {
		isDraggingPlayhead = false;
	}

	function scrubToX(x: number) {
		// Magnetic snapping: if within 8px of a deploy pin, snap directly to it!
		for (const d of studio.deploys) {
			const dx = timeToX(d.timestamp_unix_nano / 1e6);
			if (Math.abs(x - dx) < 8) {
				studio.seekTo(d.timestamp_unix_nano / 1e6);
				return;
			}
		}

		const timeMs = xToTime(x);
		studio.seekTo(timeMs);
	}

	function handleWheel(e: WheelEvent) {
		e.preventDefault();
		const zoomFactor = e.deltaY > 0 ? 1.15 : 0.85;
		const span = studio.toMs - studio.fromMs;
		const cursorRatio = e.offsetX / width;
		const cursorTime = studio.fromMs + cursorRatio * span;

		const newSpan = Math.max(10000, span * zoomFactor); // Min 10s span
		studio.fromMs = cursorTime - cursorRatio * newSpan;
		studio.toMs = cursorTime + (1 - cursorRatio) * newSpan;
		studio.fetchOverview();
	}
</script>

<div
	bind:this={container}
	data-testid="daw-timeline"
	class="relative w-full select-none overflow-hidden border-t border-white/10 bg-[#090B10]"
>
	<canvas
		bind:this={canvas}
		class="h-[260px] w-full cursor-pointer"
		onmousedown={handleMouseDown}
		onmousemove={handleMouseMove}
		onmouseup={handleMouseUp}
		onmouseleave={handleMouseUp}
		onwheel={handleWheel}
	></canvas>

	<!-- Semantic Test ID Hooks for Automated Tests -->
	<div data-testid="track-deploy-pins" class="sr-only">Track 1 Deploy Pins</div>
	<div data-testid="track-waveform" class="sr-only">Track 2 Latency Waveform</div>

	<!-- Hover Tooltip for Deploy Pin -->
	{#if hoveredDeploy && tooltipPos}
		<div
			class="pointer-events-none fixed z-50 rounded border border-sky-500/40 bg-[#0D1117]/95 p-3 font-mono text-xs text-sky-200 shadow-2xl backdrop-blur-md"
			style="left: {tooltipPos.x + 12}px; top: {tooltipPos.y - 40}px;"
		>
			<div class="flex items-center gap-2 font-bold text-sky-400">
				<span>DEPLOY: {hoveredDeploy.service_name}</span>
				<span class="rounded bg-sky-950 px-1.5 py-0.5 text-[10px] text-sky-300">
					{hoveredDeploy.commit_hash.slice(0, 7)}
				</span>
			</div>
			<div class="mt-1 text-slate-300">{hoveredDeploy.commit_message}</div>
			<div class="mt-1 text-[10px] text-slate-400">
				Author: {hoveredDeploy.author} • Env: {hoveredDeploy.environment}
			</div>
			<div class="mt-1 text-[10px] text-sky-400">Click pin to snap playhead</div>
		</div>
	{/if}
</div>

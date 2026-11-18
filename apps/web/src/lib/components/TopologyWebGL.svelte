<script lang="ts">
	import { onMount } from 'svelte';
	import { studio } from '../stores/telemetry.svelte';
	import type { ServiceNode, ServiceEdge } from '../types';

	let canvas: HTMLCanvasElement;
	let width = $state(800);
	let height = $state(450);
	let animationFrameId: number;

	// 12-Service DAG Fixed Layout Anchors (Normalized 0.0 - 1.0)
	const serviceLayoutMap: Record<string, { x: number; y: number; label: string }> = {
		'edge-cdn': { x: 0.08, y: 0.5, label: 'Edge CDN' },
		'api-gateway': { x: 0.22, y: 0.5, label: 'API Gateway' },
		'auth-svc': { x: 0.38, y: 0.2, label: 'Auth Svc' },
		'catalog-svc': { x: 0.38, y: 0.4, label: 'Catalog Svc' },
		'cart-svc': { x: 0.38, y: 0.6, label: 'Cart Svc' },
		'checkout-svc': { x: 0.38, y: 0.8, label: 'Checkout Svc' },
		'fraud-engine': { x: 0.58, y: 0.65, label: 'Fraud Engine' },
		'inventory-svc': { x: 0.58, y: 0.8, label: 'Inventory Svc' },
		'notification-svc': { x: 0.58, y: 0.95, label: 'Notification' },
		'payment-worker': { x: 0.58, y: 0.4, label: 'Payment Worker' },
		'redis-cache': { x: 0.82, y: 0.32, label: 'Redis Cache' },
		'postgres-primary': { x: 0.82, y: 0.52, label: 'Postgres Primary' }
	};

	// Animated particle state for edge flow
	interface EdgeParticle {
		edgeKey: string;
		progress: number;
		speed: number;
	}
	let particles: EdgeParticle[] = [];

	// Spring state for smooth 60fps morphing
	const currentRadii: Record<string, number> = {};
	const currentColors: Record<string, [number, number, number]> = {};

	onMount(() => {
		const ctx = canvas.getContext('2d');
		if (!ctx) return;

		// Initialize particles
		for (let i = 0; i < 40; i++) {
			particles.push({
				edgeKey: '',
				progress: Math.random(),
				speed: 0.005 + Math.random() * 0.008
			});
		}

		const resizeObserver = new ResizeObserver((entries) => {
			for (const entry of entries) {
				width = entry.contentRect.width;
				height = entry.contentRect.height;
				const dpr = window.devicePixelRatio || 1;
				canvas.width = width * dpr;
				canvas.height = height * dpr;
				ctx.scale(dpr, dpr);
			}
		});
		resizeObserver.observe(canvas.parentElement || canvas);

		let time = 0;
		const render = () => {
			time += 0.03;
			draw(ctx, time);
			animationFrameId = requestAnimationFrame(render);
		};
		animationFrameId = requestAnimationFrame(render);

		return () => {
			cancelAnimationFrame(animationFrameId);
			resizeObserver.disconnect();
		};
	});

	function draw(ctx: CanvasRenderingContext2D, time: number) {
		ctx.clearRect(0, 0, width, height);

		// Dark Cockpit Grid background
		ctx.strokeStyle = 'rgba(255, 255, 255, 0.03)';
		ctx.lineWidth = 1;
		const gridSize = 40;
		for (let x = 0; x < width; x += gridSize) {
			ctx.beginPath();
			ctx.moveTo(x, 0);
			ctx.lineTo(x, height);
			ctx.stroke();
		}
		for (let y = 0; y < height; y += gridSize) {
			ctx.beginPath();
			ctx.moveTo(0, y);
			ctx.lineTo(width, y);
			ctx.stroke();
		}

		const activeState = studio.currentState;
		const nodeDataMap = new Map<string, ServiceNode>();
		if (activeState?.nodes) {
			for (const n of activeState.nodes) {
				const nName = n.name || (n as any).Name;
				nodeDataMap.set(nName, n);
			}
		}

		const edgeList = activeState?.edges || [];
		const isIsolated = studio.selectedService !== null;

		// 1. Draw Edges
		for (const edge of edgeList) {
			const srcKey = edge.source || (edge as any).Source;
			const tgtKey = edge.target || (edge as any).Target;
			const p1 = serviceLayoutMap[srcKey];
			const p2 = serviceLayoutMap[tgtKey];
			if (!p1 || !p2) continue;

			const x1 = p1.x * width;
			const y1 = p1.y * height;
			const x2 = p2.x * width;
			const y2 = p2.y * height;

			const isEdgeFailing = edge.is_failing ?? (edge as any).IsFailing ?? false;
			const isConnected =
				!isIsolated || srcKey === studio.selectedService || tgtKey === studio.selectedService;

			ctx.save();
			if (!isConnected) {
				ctx.globalAlpha = 0.12;
			}

			// Bezier curve
			const cp1x = x1 + (x2 - x1) * 0.5;
			const cp1y = y1;
			const cp2x = x1 + (x2 - x1) * 0.5;
			const cp2y = y2;

			ctx.beginPath();
			ctx.moveTo(x1, y1);
			ctx.bezierCurveTo(cp1x, cp1y, cp2x, cp2y, x2, y2);

			if (isEdgeFailing) {
				ctx.strokeStyle = 'rgba(239, 68, 68, 0.85)';
				ctx.lineWidth = 2.5;
				ctx.shadowColor = '#EF4444';
				ctx.shadowBlur = 8;
			} else {
				ctx.strokeStyle = 'rgba(16, 185, 129, 0.35)';
				ctx.lineWidth = 1.5;
				ctx.shadowBlur = 0;
			}
			ctx.stroke();
			ctx.restore();
		}

		// 2. Draw Moving Flow Particles
		ctx.save();
		for (let i = 0; i < particles.length; i++) {
			const p = particles[i];
			const edge = edgeList[i % (edgeList.length || 1)];
			if (!edge) continue;

			const srcKey = edge.source || (edge as any).Source;
			const tgtKey = edge.target || (edge as any).Target;
			const p1 = serviceLayoutMap[srcKey];
			const p2 = serviceLayoutMap[tgtKey];
			if (!p1 || !p2) continue;

			p.progress += p.speed;
			if (p.progress > 1) p.progress = 0;

			const x1 = p1.x * width;
			const y1 = p1.y * height;
			const x2 = p2.x * width;
			const y2 = p2.y * height;
			const tVal = p.progress;

			// Evaluate cubic bezier at tVal
			const cx = 3 * (x1 + (x2 - x1) * 0.5 - x1);
			const bx = 3 * (x1 + (x2 - x1) * 0.5 - (x1 + (x2 - x1) * 0.5)) - cx;
			const ax = x2 - x1 - cx - bx;

			const cy = 3 * (y1 - y1);
			const by = 3 * (y2 - y1) - cy;
			const ay = y2 - y1 - cy - by;

			const px = (ax * tVal * tVal * tVal + bx * tVal * tVal + cx * tVal + x1);
			const py = (ay * tVal * tVal * tVal + by * tVal * tVal + cy * tVal + y1);

			const isEdgeFailing = edge.is_failing ?? (edge as any).IsFailing ?? false;
			ctx.beginPath();
			ctx.arc(px, py, isEdgeFailing ? 3.0 : 2.0, 0, Math.PI * 2);
			ctx.fillStyle = isEdgeFailing ? '#EF4444' : '#10B981';
			ctx.shadowColor = isEdgeFailing ? '#EF4444' : '#10B981';
			ctx.shadowBlur = 6;
			ctx.fill();
		}
		ctx.restore();

		// 3. Draw Nodes
		for (const [key, layout] of Object.entries(serviceLayoutMap)) {
			const x = layout.x * width;
			const y = layout.y * height;
			const node = nodeDataMap.get(key);

			const rps = node?.rps || (node as any)?.RPS || 10;
			const errRate = node?.error_rate || (node as any)?.ErrorRate || 0;
			const p95 = node?.p95_latency_ms || (node as any)?.P95LatencyMs || 25;
			const isRootCause =
				node?.is_root_cause ||
				(node as any)?.IsRootCause ||
				studio.topSuspect?.service_name === key;

			// Target radius scaled by RPS (16px to 32px)
			const targetRadius = 16 + Math.min(16, (rps / 100) * 16);
			currentRadii[key] = currentRadii[key]
				? currentRadii[key] + (targetRadius - currentRadii[key]) * 0.15
				: targetRadius;
			const r = currentRadii[key];

			// Color selection
			let nodeColor = '#10B981'; // Emerald
			let ringGlow = 'rgba(16, 185, 129, 0.4)';
			if (isRootCause) {
				nodeColor = '#EF4444'; // Pulsing Crimson
				ringGlow = 'rgba(239, 68, 68, 0.8)';
			} else if (errRate > 0.05 || p95 > 1000) {
				nodeColor = '#EF4444';
				ringGlow = 'rgba(239, 68, 68, 0.6)';
			} else if (errRate > 0.01 || p95 > 300) {
				nodeColor = '#F59E0B'; // Amber
				ringGlow = 'rgba(245, 158, 11, 0.5)';
			}

			const isSelected = studio.selectedService === key;
			const isDimmed = isIsolated && !isSelected;

			ctx.save();
			if (isDimmed) {
				ctx.globalAlpha = 0.2;
			}

			// Root cause expanding shockwave pulse
			if (isRootCause) {
				const pulseR = r + 8 + Math.sin(time * 4) * 6;
				ctx.beginPath();
				ctx.arc(x, y, pulseR, 0, Math.PI * 2);
				ctx.strokeStyle = 'rgba(239, 68, 68, 0.5)';
				ctx.lineWidth = 2;
				ctx.stroke();
			}

			// Outer Glow Halo
			ctx.beginPath();
			ctx.arc(x, y, r + 4, 0, Math.PI * 2);
			ctx.strokeStyle = ringGlow;
			ctx.lineWidth = isSelected ? 3.5 : 2;
			ctx.shadowColor = nodeColor;
			ctx.shadowBlur = isRootCause ? 18 : 8;
			ctx.stroke();

			// Core Node Fill
			ctx.beginPath();
			ctx.arc(x, y, r, 0, Math.PI * 2);
			ctx.fillStyle = '#0D1117';
			ctx.fill();
			ctx.lineWidth = 1.5;
			ctx.strokeStyle = nodeColor;
			ctx.stroke();

			// Node Label
			ctx.shadowBlur = 0;
			ctx.font = '600 11px "JetBrains Mono", monospace';
			ctx.fillStyle = '#F3F4F6';
			ctx.textAlign = 'center';
			ctx.fillText(layout.label, x, y - r - 8);

			// Metrics Subtitle
			ctx.font = '400 9px "JetBrains Mono", monospace';
			ctx.fillStyle = errRate > 0.02 ? '#EF4444' : '#9CA3AF';
			ctx.fillText(`${p95.toFixed(0)}ms | ${rps.toFixed(0)} rps`, x, y + r + 13);

			// Root cause chip
			if (isRootCause) {
				ctx.fillStyle = '#EF4444';
				ctx.font = '700 8px "JetBrains Mono", monospace';
				ctx.fillText('SUSPECT ROOT CAUSE', x, y + r + 24);
			}

			ctx.restore();
		}
	}

	function handleCanvasClick(e: MouseEvent) {
		const rect = canvas.getBoundingClientRect();
		const clickX = e.clientX - rect.left;
		const clickY = e.clientY - rect.top;

		for (const [key, layout] of Object.entries(serviceLayoutMap)) {
			const x = layout.x * width;
			const y = layout.y * height;
			const r = currentRadii[key] || 20;

			const dist = Math.hypot(clickX - x, clickY - y);
			if (dist <= r + 6) {
				studio.isolateService(key);
				return;
			}
		}
		studio.isolateService(null);
	}
</script>

<div class="relative h-full w-full select-none overflow-hidden bg-[#090B10]">
	<canvas
		bind:this={canvas}
		data-testid="topology-canvas"
		class="h-full w-full cursor-crosshair"
		onclick={handleCanvasClick}
	></canvas>

	<!-- Cockpit HUD Overlay -->
	<div class="pointer-events-none absolute top-3 left-4 flex items-center gap-3">
		<span class="inline-flex items-center gap-1.5 rounded border border-white/10 bg-[#111622]/80 px-2.5 py-1 text-xs font-mono font-medium text-emerald-400 backdrop-blur">
			<span class="h-2 w-2 rounded-full bg-emerald-500 animate-pulse"></span>
			60 FPS TOPOLOGY
		</span>

		{#if studio.selectedService}
			<span class="inline-flex items-center gap-1.5 rounded border border-cyan-500/30 bg-cyan-950/60 px-2.5 py-1 text-xs font-mono text-cyan-300 backdrop-blur">
				BLAST RADIUS: {studio.selectedService}
			</span>
		{/if}

		{#if studio.topSuspect}
			<span class="inline-flex items-center gap-1.5 rounded border border-rose-500/30 bg-rose-950/60 px-2.5 py-1 text-xs font-mono font-semibold text-rose-300 backdrop-blur">
				CRITICAL: {studio.topSuspect.service_name} ({(studio.topSuspect.confidence * 100).toFixed(0)}%)
			</span>
		{/if}
	</div>
</div>

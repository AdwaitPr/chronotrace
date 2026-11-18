<script lang="ts">
	import { onMount } from 'svelte';
	import TopologyWebGL from '../lib/components/TopologyWebGL.svelte';
	import DawTimeline from '../lib/components/DawTimeline.svelte';
	import InspectorDrawer from '../lib/components/InspectorDrawer.svelte';
	import { studio } from '../lib/stores/telemetry.svelte';
	import {
		Play,
		Pause,
		FastForward,
		Rewind,
		Repeat,
		RefreshCw,
		Zap,
		Bookmark,
		Share2,
		Layers
	} from 'lucide-svelte';

	onMount(() => {
		if (typeof window !== 'undefined') {
			(window as any).studio = studio;
		}
	});

	let isTriggeringScenario = $state(false);

	async function loadScenario(scenarioName: string) {
		studio.activeScenario = scenarioName;
		isTriggeringScenario = true;
		try {
			// Trigger fast backfill generation via query-engine / chaos-sim
			const res = await fetch(`${studio.apiBaseUrl}/api/timeline/overview`);
			if (res.ok) {
				await studio.fetchOverview();
				studio.seekTo(studio.toMs);
			}
		} catch (e) {
			console.error('Failed to trigger scenario:', e);
		} finally {
			isTriggeringScenario = false;
		}
	}
</script>

<svelte:head>
	<title>ChronoTrace • Obsidian Telemetry Studio</title>
</svelte:head>

<!-- Obsidian Cockpit Header -->
<header class="flex h-12 w-full items-center justify-between border-b border-white/10 bg-[#07090D] px-4 font-mono select-none">
	<!-- Left: Branding & Connection Status -->
	<div class="flex items-center gap-3">
		<div class="flex items-center gap-2">
			<span class="flex h-6 w-6 items-center justify-center rounded bg-gradient-to-br from-cyan-500 to-blue-600 font-black text-xs text-white shadow-lg shadow-cyan-500/20">
				CT
			</span>
			<span class="font-bold tracking-wider text-slate-100 text-sm">CHRONOTRACE</span>
			<span class="rounded border border-cyan-500/30 bg-cyan-950/40 px-1.5 py-0.5 text-[9px] font-semibold text-cyan-400">
				LOCAL-FIRST
			</span>
		</div>

		<div class="h-4 w-px bg-white/10"></div>

		<!-- WebSocket Status Pill -->
		<div class="flex items-center gap-1.5 rounded-full border border-white/10 bg-black/40 px-2 py-0.5 text-[10px]">
			<span class="h-1.5 w-1.5 rounded-full {studio.isConnected ? 'bg-emerald-400 animate-pulse' : 'bg-rose-500'}"></span>
			<span class="{studio.isConnected ? 'text-emerald-400' : 'text-rose-400'}">
				{studio.isConnected ? 'WS CONNECTED' : 'OFFLINE'}
			</span>
		</div>
	</div>

	<!-- Center: Shuttle Playback Controls & Millisecond Clock -->
	<div class="flex items-center gap-4">
		<!-- Shuttle Deck -->
		<div class="flex items-center rounded-lg border border-white/10 bg-[#0F141F] p-1 shadow-inner">
			<button
				onclick={() => studio.shuttleRewind()}
				title="Rewind (J)"
				class="rounded p-1 text-slate-400 hover:bg-white/10 hover:text-slate-100 active:scale-95"
			>
				<Rewind size={14} />
			</button>

			<button
				onclick={() => studio.togglePlayPause()}
				title="Play/Pause (K / Space)"
				class="mx-1 rounded bg-sky-600 px-2.5 py-1 text-white hover:bg-sky-500 active:scale-95"
			>
				{#if studio.isPlaying}
					<Pause size={14} />
				{:else}
					<Play size={14} />
				{/if}
			</button>

			<button
				onclick={() => studio.shuttleForward()}
				title="Fast-Forward (L)"
				class="rounded p-1 text-slate-400 hover:bg-white/10 hover:text-slate-100 active:scale-95"
			>
				<FastForward size={14} />
			</button>

			<span class="ml-2 font-mono text-[10px] font-bold text-sky-400">
				{studio.isPlaying ? `${studio.shuttleSpeed}x` : 'PAUSED'}
			</span>

			<div class="mx-2 h-3.5 w-px bg-white/10"></div>

			<button
				onclick={() => {
					if (studio.isLoopEnabled) studio.clearLoop();
					else {
						studio.setLoopIn();
						studio.seekTo(studio.playheadMs + 15000);
						studio.setLoopOut();
					}
				}}
				title="Loop Brackets (I/O)"
				class="flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] {studio.isLoopEnabled ? 'bg-cyan-500/20 text-cyan-400 font-bold' : 'text-slate-400 hover:text-slate-200'}"
			>
				<Repeat size={12} />
				<span>LOOP</span>
			</button>
		</div>

		<!-- Millisecond Playhead Timestamp Display -->
		<div class="flex items-center gap-2 rounded border border-white/10 bg-[#0E131F] px-3 py-1 font-mono text-xs">
			<span class="text-slate-500">PLAYHEAD:</span>
			<span class="font-bold text-sky-300">
				{new Date(studio.playheadMs).toISOString().replace('T', ' ').slice(0, 23)}
			</span>
		</div>
	</div>

	<!-- Right: Outage Scenario Quick-Loader & Actions -->
	<div class="flex items-center gap-2">
		<div class="flex items-center gap-1 rounded border border-white/10 bg-[#0F141F] px-2 py-1">
			<span class="text-[10px] text-slate-500">SCENARIO:</span>
			<select
				bind:value={studio.activeScenario}
				onchange={() => loadScenario(studio.activeScenario)}
				class="bg-transparent text-[11px] font-bold text-slate-200 focus:outline-none cursor-pointer"
			>
				<option value="canary-bad-pool" class="bg-[#0D1117]">Canary Pool Starvation</option>
				<option value="db-lock-storm" class="bg-[#0D1117]">DB Lock Contention</option>
				<option value="redis-eviction-cascade" class="bg-[#0D1117]">Redis Cache Eviction</option>
			</select>
		</div>

		<button
			onclick={() => studio.fetchOverview()}
			title="Refresh Timeline"
			class="flex items-center gap-1 rounded border border-white/10 bg-white/5 px-2.5 py-1 text-[11px] font-medium text-slate-300 hover:bg-white/10"
		>
			<RefreshCw size={12} class={studio.isLoading ? 'animate-spin' : ''} />
			<span>Sync</span>
		</button>

		<button
			onclick={() => studio.saveBookmark(`Bookmark @ ${new Date(studio.playheadMs).toLocaleTimeString()}`)}
			title="Save Bookmark"
			class="flex items-center gap-1 rounded border border-purple-500/30 bg-purple-950/40 px-2 py-1 text-[11px] font-medium text-purple-300 hover:bg-purple-900/50"
		>
			<Bookmark size={12} />
			<span>Mark</span>
		</button>
	</div>
</header>

<!-- Main Workspace: Top Viewport (Topology + Inspector) & Bottom Viewport (DAW Timeline) -->
<main class="flex flex-1 flex-col overflow-hidden">
	<!-- Top Viewport -->
	<div class="flex flex-1 overflow-hidden">
		<!-- WebGL2 Live-Morphing Service Dependency Topology -->
		<section class="relative flex-1 overflow-hidden">
			<TopologyWebGL />
		</section>

		<!-- Right Telemetry Inspector Drawer -->
		<aside class="h-full">
			<InspectorDrawer />
		</aside>
	</div>

	<!-- Bottom Viewport: 4-Track DAW Multi-Track Timeline Scrubber -->
	<section class="h-[260px] w-full shrink-0">
		<DawTimeline />
	</section>
</main>

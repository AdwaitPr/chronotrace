<script lang="ts">
	import { studio } from '../stores/telemetry.svelte';
	import type { LogEntry, CausalScore } from '../types';

	let logFilter = $state<'ALL' | '5XX' | 'SLOW'>('ALL');
	let noteTitle = $state('');
	let noteContent = $state('');
	let isAddingNote = $state(false);

	const logs = $derived(studio.currentState?.recent_logs || []);
	const filteredLogs = $derived.by(() => {
		if (logFilter === '5XX') {
			return logs.filter((l) => l.level === 'ERROR' || l.message.includes('50'));
		}
		if (logFilter === 'SLOW') {
			return logs.filter((l) => l.message.includes('timeout') || l.message.includes('slow'));
		}
		return logs;
	});

	const suspects = $derived(studio.currentState?.suspects || []);

	async function submitNote() {
		if (!noteTitle.trim()) return;
		await studio.addNote(noteTitle, noteContent);
		noteTitle = '';
		noteContent = '';
		isAddingNote = false;
	}

	function jumpToDeploy(d?: any) {
		if (!d) return;
		const ms = d.timestamp_unix_nano / 1e6;
		studio.seekTo(ms);
	}
</script>

<div
	data-testid="log-inspector-drawer"
	class="flex h-full w-[420px] flex-col border-l border-white/10 bg-[#0B0F17] font-mono text-xs text-slate-200"
>
	<!-- Drawer Header Tabs -->
	<div class="flex items-center justify-between border-b border-white/10 px-4 py-2.5 bg-[#090B10]">
		<div class="flex items-center gap-2">
			<span class="font-bold text-slate-100">TELEMETRY INSPECTOR</span>
			<span class="rounded bg-sky-950 px-1.5 py-0.5 text-[10px] text-sky-400">PLAYHEAD SYNC</span>
		</div>
		<div class="text-[11px] text-slate-400">
			{new Date(studio.playheadMs).toISOString().slice(11, 23)}
		</div>
	</div>

	<!-- Section 1: Suspect Root-Cause Confidence Card -->
	<div data-testid="root-cause-card" class="border-b border-white/10 p-4 bg-[#0E131F]/50">
		<div class="flex items-center justify-between">
			<span class="font-semibold text-rose-400">CAUSAL ROOT-CAUSE RANKING</span>
			<span class="text-[10px] text-slate-400">5-MIN CAUSAL WINDOW</span>
		</div>

		{#if suspects.length > 0}
			<div class="mt-3 space-y-3">
				{#each suspects.slice(0, 3) as suspect, idx}
					<div
						class="rounded-lg border {idx === 0 ? 'border-rose-500/40 bg-rose-950/20' : 'border-white/10 bg-black/30'} p-3"
					>
						<div class="flex items-center justify-between">
							<div class="flex items-center gap-2">
								<span class="flex h-5 w-5 items-center justify-center rounded-full {idx === 0 ? 'bg-rose-500 text-white' : 'bg-slate-700 text-slate-300'} font-bold text-[10px]">
									#{idx + 1}
								</span>
								<span class="font-bold text-slate-100">{suspect.service_name}</span>
							</div>
							<span class="font-bold {idx === 0 ? 'text-rose-400' : 'text-slate-300'}">
								{(suspect.confidence * 100).toFixed(1)}% CONF
							</span>
						</div>

						<!-- Confidence progress bar -->
						<div class="mt-2 h-1.5 w-full overflow-hidden rounded-full bg-slate-800">
							<div
								class="h-full rounded-full {idx === 0 ? 'bg-rose-500' : 'bg-amber-500'}"
								style="width: {suspect.confidence * 100}%"
							></div>
						</div>

						<p class="mt-2 text-[11px] text-slate-300 leading-relaxed">{suspect.reason}</p>

						{#if suspect.blast_radius && suspect.blast_radius.length > 0}
							<div class="mt-2 flex flex-wrap items-center gap-1">
								<span class="text-[9px] text-slate-500">BLAST RADIUS:</span>
								{#each suspect.blast_radius as br}
									<span class="rounded bg-rose-900/40 px-1.5 py-0.5 text-[9px] text-rose-300">
										{br}
									</span>
								{/each}
							</div>
						{/if}

						<div class="mt-3 flex items-center gap-2">
							{#if suspect.deploy_event}
								<button
									onclick={() => jumpToDeploy(suspect.deploy_event)}
									class="rounded bg-sky-900/40 px-2 py-1 text-[10px] font-semibold text-sky-300 hover:bg-sky-800/60"
								>
									Jump to Commit ({suspect.deploy_event.commit_hash.slice(0, 7)})
								</button>
							{/if}
							<button
								onclick={() => studio.isolateService(suspect.service_name)}
								class="rounded bg-slate-800 px-2 py-1 text-[10px] font-semibold text-slate-300 hover:bg-slate-700"
							>
								{studio.selectedService === suspect.service_name ? 'Clear Isolation' : 'Isolate Topology'}
							</button>
						</div>
					</div>
				{/each}
			</div>
		{:else}
			<div class="mt-3 rounded border border-white/5 bg-black/20 p-3 text-center text-slate-500">
				No anomalous incident detected at active playhead timestamp.
			</div>
		{/if}
	</div>

	<!-- Section 2: Synchronized Logs & Spans -->
	<div class="flex flex-1 flex-col overflow-hidden">
		<div class="flex items-center justify-between border-b border-white/10 px-4 py-2 bg-[#090B10]">
			<span class="font-semibold text-slate-300">PLAYHEAD LOGS ({filteredLogs.length})</span>
			<div class="flex items-center gap-1">
				<button
					onclick={() => (logFilter = 'ALL')}
					class="rounded px-2 py-0.5 text-[10px] {logFilter === 'ALL' ? 'bg-sky-500/20 text-sky-400 font-bold' : 'text-slate-400'}"
				>
					ALL
				</button>
				<button
					onclick={() => (logFilter = '5XX')}
					class="rounded px-2 py-0.5 text-[10px] {logFilter === '5XX' ? 'bg-rose-500/20 text-rose-400 font-bold' : 'text-slate-400'}"
				>
					5XX
				</button>
				<button
					onclick={() => (logFilter = 'SLOW')}
					class="rounded px-2 py-0.5 text-[10px] {logFilter === 'SLOW' ? 'bg-amber-500/20 text-amber-400 font-bold' : 'text-slate-400'}"
				>
					SLOW
				</button>
			</div>
		</div>

		<div class="flex-1 overflow-y-auto p-3 space-y-2">
			{#each filteredLogs as log}
				<div class="rounded border border-white/5 bg-black/40 p-2 text-[11px] leading-tight hover:border-white/20">
					<div class="flex items-center justify-between text-slate-400 text-[10px]">
						<span class="font-bold {log.level === 'ERROR' ? 'text-rose-400' : 'text-sky-400'}">
							[{log.level}] {log.service_name}
						</span>
						<span>{new Date(log.timestamp_unix_nano / 1e6).toISOString().slice(14, 23)}</span>
					</div>
					<div class="mt-1 text-slate-200">{log.message}</div>
					{#if log.trace_id}
						<div class="mt-1 text-[9px] text-slate-500">Trace: {log.trace_id}</div>
					{/if}
				</div>
			{:else}
				<div class="py-6 text-center text-slate-500">No logs for active window</div>
			{/each}
		</div>

		<!-- Postmortem Notes Section -->
		<div class="border-t border-white/10 p-3 bg-[#0A0D14]">
			<div class="flex items-center justify-between">
				<span class="font-semibold text-slate-300">POSTMORTEM NOTES ({(studio.notes || []).length})</span>
				<button
					onclick={() => (isAddingNote = !isAddingNote)}
					class="rounded bg-sky-950 px-2 py-1 text-[10px] font-bold text-sky-400 hover:bg-sky-900"
				>
					{isAddingNote ? 'Cancel' : '+ Add Note'}
				</button>
			</div>

			{#if isAddingNote}
				<div class="mt-2 space-y-2">
					<input
						type="text"
						bind:value={noteTitle}
						placeholder="Note title (e.g. Bad Connection Pool Deploy)"
						class="w-full rounded border border-white/10 bg-black/50 px-2.5 py-1 text-xs text-slate-100 placeholder-slate-500 focus:border-sky-500 focus:outline-none"
					/>
					<textarea
						bind:value={noteContent}
						placeholder="Postmortem findings, blast radius observations..."
						rows="2"
						class="w-full rounded border border-white/10 bg-black/50 px-2.5 py-1 text-xs text-slate-100 placeholder-slate-500 focus:border-sky-500 focus:outline-none"
					></textarea>
					<button
						onclick={submitNote}
						class="w-full rounded bg-sky-600 py-1 font-bold text-white hover:bg-sky-500"
					>
						Save to SQLite
					</button>
				</div>
			{/if}
		</div>
	</div>
</div>

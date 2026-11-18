<script lang="ts">
	import './layout.css';
	let { children } = $props();
</script>

<div class="h-screen w-screen overflow-hidden bg-[#090B10] text-slate-100 flex flex-col">
	{@render children()}
</div>

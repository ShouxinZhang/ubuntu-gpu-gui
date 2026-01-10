function $(id) {
  return document.getElementById(id);
}

function fmtPower(ov) {
  if (!ov.pwrDraw) return "--";
  return `${ov.pwrDraw} / ${ov.pwrEnforced} W`;
}

async function waitForBindings(timeoutMs = 8000) {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    if (window.go && window.go.main && window.go.main.App && window.go.main.App.GetOverview) {
      return;
    }
    await new Promise((r) => setTimeout(r, 50));
  }
  throw new Error("Wails bindings not available (window.go.main.App)");
}

async function refreshOnce() {
  const ov = await window.go.main.App.GetOverview();

  $("dgpu").textContent = ov.nvidiaName || "--";
  $("igpu").textContent = ov.iGpuText || "--";
  $("util").textContent = ov.util ?? "--";
  $("temp").textContent = ov.temp ?? "--";
  $("mem").textContent = ov.memUsed && ov.memTotal ? `${ov.memUsed} / ${ov.memTotal} MiB` : "--";
  $("pwr").textContent = fmtPower(ov);
  $("powerd").textContent = ov.powerdStatus || "--";
  $("procs").textContent = ov.processes || "";

  if (ov.error) {
    console.error(`NVIDIA error: ${ov.error}`);
  }
}

async function refreshLimits() {
  const lim = await window.go.main.App.GetPowerLimits();
  if (lim.error) {
    $("limits").textContent = `Power limit: ${lim.error}`;
    return;
  }
  $("limits").textContent = `Power limit (W): enforced=${Math.round(lim.enforced)} default=${Math.round(lim.default)} max=${Math.round(lim.max)}`;
}

async function runAction(fn) {
  $("btnEnablePowerd").disabled = true;
  $("btnMaxPower").disabled = true;
  $("btnRefresh").disabled = true;
  try {
    const res = await fn();
    if (!res) {
      $("result").textContent = "";
    } else if (res.ok) {
      $("result").textContent = res.output || "Action completed";
    } else {
      $("result").textContent = (res.error ? `Error: ${res.error}\n` : "") + (res.output || "");
    }
  } finally {
    $("btnEnablePowerd").disabled = false;
    $("btnMaxPower").disabled = false;
    $("btnRefresh").disabled = false;
  }
  await refreshLimits();
  await refreshOnce();
}

async function main() {
  await waitForBindings();

  $("btnRefresh").addEventListener("click", async () => {
    await refreshOnce();
    await refreshLimits();
  });
  $("btnEnablePowerd").addEventListener("click", () => runAction(() => window.go.main.App.EnablePowerd()));
  $("btnMaxPower").addEventListener("click", () => runAction(() => window.go.main.App.EnableMaxPower()));

  await refreshOnce();
  await refreshLimits();
  setInterval(() => refreshOnce().catch(() => {}), 1000);
}

main().catch((e) => {
  console.error(String(e));
  $("result").textContent = String(e);
});

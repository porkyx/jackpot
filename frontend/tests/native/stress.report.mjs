// Contain owned report output failures so resource cleanup can continue.
// The report is the driver's owned plain object; the write closure stays lazy.
export async function writeCleanupReport(report, stage, write) {
 try {
  await write();
  return true;
 } catch {
  report.status = "failed";
  process.exitCode = 1;
  const failures = report.reportingFailures ??= [];
  if (failures.length < 3) {
   failures.push({
    stage: ["before-cleanup", "after-cleanup", "final-live"].includes(stage) ? stage : "unknown",
    code: "REPORT_OUTPUT_FAILED",
   });
  }
  return false;
 }
}
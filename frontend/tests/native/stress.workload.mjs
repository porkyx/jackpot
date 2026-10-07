import assert from 'node:assert/strict';
export function localWorkload(env = process.env) {
  const fixtureComments = Number(env.JACKPOT_STRESS_FIXTURE_COMMENTS ?? '200');
  assert.ok(fixtureComments === 100 || fixtureComments === 200, 'fixture comments must be 100 or 200');
  const participants = fixtureComments === 100 ? 41 : 100;
  const included = participants - 1;
  const maxReruns = Math.floor(included / 10) - 1;
  const drawSampleCount = Number(env.JACKPOT_STRESS_DRAW_SAMPLES ?? '3');
  assert.ok(Number.isSafeInteger(drawSampleCount) && drawSampleCount >= 1 && drawSampleCount <= maxReruns, 'reruns must fit the eligible participant union');
  const iterations = Number(env.JACKPOT_STRESS_ITERATIONS ?? '20');
  assert.ok(Number.isSafeInteger(iterations) && iterations >= 1 && iterations <= 100, 'scope iterations must be 1..100');
  return Object.freeze({fixtureComments, participants, included, initialRows: participants, drawSampleCount, iterations, totalRounds: drawSampleCount + 1, totalWinners: (drawSampleCount + 1) * 10, statisticalSampleSufficient: drawSampleCount >= 20});
}

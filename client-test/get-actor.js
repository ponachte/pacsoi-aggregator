import { URL } from 'url';

const ACTOR_ENDPOINT = `http://localhost:5000/actors/observations`;
const ACTOR_URL = new URL(ACTOR_ENDPOINT);

async function main() {
  console.log(`=== Requesting actor results at ${ACTOR_URL}`);

  const actorResultsResponse = await fetch(ACTOR_URL, {
      method: "GET"
  });

  console.log(`=== Actor results response status: ${actorResultsResponse.status}`);
  if (actorResultsResponse.status !== 200) {
      console.error(`Error: ${actorResultsResponse.status}, response: ${await actorResultsResponse.text()}`);
      return;
  }
  const actorResults = await actorResultsResponse.json();
  console.log(`= Actor results:\n${JSON.stringify(actorResults, null, 2)}\n`);
}

main()
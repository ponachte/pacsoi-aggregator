import { URL } from 'url';

const ACTORS_ENDPOINT = 'http://localhost:5000/actors'
const CONFIG_ENDPOINT = 'http://localhost:5000/config/actors';
const CONFIG_URL = new URL(CONFIG_ENDPOINT);

async function main() {
    console.log(`=== Requesting actors at ${CONFIG_URL}`);

    const response = await fetch(CONFIG_URL, {
        method: "GET"
    });
    console.log(`=== Response status: ${response.status}`);

    if (response.status !== 200) {
        console.error(`Error: ${response.status}, response: ${await response.text()}`);
        return;
    }

    const body = await response.json();
    const actorIds = body.actors;
    console.log(`=== Found ${actorIds.length} actor(s)`);

    for (const id of actorIds) {
        const actorConfigUrl = `${CONFIG_ENDPOINT}/${id}`;
        console.log(`=== Requesting actor config at ${actorConfigUrl}`);

        const actorResponse = await fetch(actorConfigUrl, {
            method: "GET"
        });
        console.log(`=== Actor config response status: ${actorResponse.status}`);

        if (actorResponse.status !== 200) {
            console.error(`Error: ${actorResponse.status}, response: ${await actorResponse.text()}`);
            continue; // skip this actor and continue with the next
        }

        const actorConfig = await actorResponse.json();
        console.log(JSON.stringify(actorConfig, null, 2));
    }
}

main();

# Aggregator

An aggregator using uma: https://github.com/SolidLabResearch/user-managed-access as the authorization server.

## Requirements
This project requires a kubernetes cluster and a running uma server.

### Kubernetes Cluster
install a kubernetes cluster with minikube:
```bash
curl -LO https://storage.googleapis.com/minikube/releases/latest/minikube-linux-amd64
sudo install minikube-linux-amd64 /usr/local/bin/minikube
```

when minikube is installed, initialize it:
```bash
make minikube-init
```
This will start the minikube cluster, build all the containers, and load them into the minikube cluster.
To only build or load the containers without starting the cluster, you can run:
```bash
make containers-build # Build the containers
make containers-load # Load the containers into the minikube cluster
make containers-all # Build and load the containers
```
To stop and delete the minikube cluster, you can run:
```bash
make minikube-stop
```

### uma Server
To install the uma server, you first need to clone the uma repository:
```bash
git clone https://github.com/SolidLabResearch/user-managed-access
cd user-managed-access
```
Make sure you have node.js and npm installed with a version of at least 20.0.0, and run `corepack enable`.
Then install the dependencies:
```bash
yarn install
```
Finally, the uma server can be started with:
```bash
cd packages/uma
yarn start
```

The UMA Server must be configured so a client can access the config endpoint and read all the created actors:
```
@prefix ex: <http://example.org/1707120963224#> .
@prefix odrl: <http://www.w3.org/ns/odrl/2/> .
@prefix odrl_p: <https://w3id.org/force/odrl3proposal#> .

ex:usagePolicy1 a odrl:Agreement ;
                odrl:permission ex:permission1 .
ex:permission1 a odrl:Permission2 ;
               odrl:action odrl:read , odrl:create , odrl:modify ;
               odrl:target <http://localhost:5000/config/actors/> ;
               odrl:assignee <$CLIENT-WEBID> ;
               odrl:assigner <$ASSIGNER-WEBID> .

ex:usagePolicy2 a odrl:Agreement ;
                odrl:permission ex:permission2 .
ex:permission2 a odrl:Permission ;
               odrl:action odrl:read ;
               odrl:target <collection:actors/> ;
               odrl:assignee <$CLIENT-WEBID> ;
               odrl:assigner <$ASSIGNER-WEBID> .

<collection:actors/> a odrl:AssetCollection ;
  odrl:source <http://localhost:5000/actors> ;
  odrl_p:relation <http://www.w3.org/ns/ldp#contains> .
```

## Run the Aggregator
To deploy the aggregator into the minikube server first update the `k8s/aggregator.yaml` deployment file.
Add the kubernetes cluster IP and the UMA server IP as environment variables
```bash
make minikube-deploy
```
The aggregator is available on `<$MINIKUBE-IP>:30500`.

To clean the aggregator deployment run:
```bash
make minikube-clean
```

## WSL
In WSL Kubernetes NodePorts might not work, so the aggregator is not reachable via `<$MINIKUBE-IP>:30500`.
In this case u must use port-forwarding to expose the aggregator:
```bash
make expose-aggregator
```
The aggregator is now available at `http://localhost:5000`.
Stop the port forwarding with:
```bash
make stop-aggregator
```

### Demo
An easy way to test the aggregator is by running `node client-test/create-actor.js` to create an actor.
Do make sure the uma server is running before you do this, and that it has the correct policies so you can access the correct endpoints.
After that, you can run `node client-test/get-actor.js` to retrieve the info on the actor you just created and its results.

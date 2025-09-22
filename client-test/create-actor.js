
import { URL } from 'url';

const TOKEN_URL = 'https://kvasir-auth.faqir.org/realms/quarkus/protocol/openid-connect/token';
const CLIENT_ID = 'aggregator';
const CLIENT_SECRET = 'SubNkF1qZ0UkPmhr67YNOSLXNxF2mtuW';
const QUERY_ENDPOINT = 'https://kvasir.faqir.org/pol/slices/persons/query';

const SPARQL_QUERY = `
PREFIX ex: <http://example.org/>
PREFIX schema: <http://schema.org/>
SELECT ?n1 WHERE {
  ?p1 schema:givenName ?n1 ;
    ex:knows ?p2 .
  ?p2 schema:givenName "Kenneth" .
}`

const SCHEMA_SOURCE = `type Query {
  persons(cursor: String): [foaf_Person]!
  person(id: ID!, cursor: String): foaf_Person
}

type foaf_Person {
  id(cursor: String): ID!
  ex_knows(id: ID, cursor: String): [foaf_Person]!
  schema_email(cursor: String): String!
  schema_givenName(cursor: String): String!
}`

const SCHEMA_CONTEXT = {
  "kss": "https://kvasir.discover.ilabt.imec.be/vocab#",
  "ex": "http://example.org/",
  "foaf": "http://xmlns.com/foaf/0.1/",
  "schema": "http://schema.org/"
};

const PipelineDescription = `
    @prefix config: <http://localhost:5000/config#> .
    @prefix fno: <https://w3id.org/function/ontology#> .
    @prefix xsd: <http://www.w3.org/2001/XMLSchema#> .
    @prefix rdf: <http://www.w3.org/1999/02/22-rdf-syntax-ns#> .
    @prefix ex: <http://example.org/> .
    _:execution a fno:Execution ;
        fno:executes config:SPARQLEvaluation ;
        config:sources ( ex:kvasirSource ) ;
        config:queryString """${SPARQL_QUERY}"""^^xsd:string ;
        config:client "${CLIENT_ID}"^^xsd:string ;
        config:secret "${CLIENT_SECRET}"^^xsd:string ;
        config:tokenUrl "${TOKEN_URL}"^^xsd:string .
    
    ex:kvasirSource a config:KvasirSource ;
        config:url "${QUERY_ENDPOINT}"^^xsd:string ;
        config:schema """${SCHEMA_SOURCE}"""^^xsd:string ;
        config:context """${JSON.stringify(SCHEMA_CONTEXT)}"""^^xsd:string .
`;

async function main() {
    const pipelineEndpoint = 'http://localhost:5000/config/actors';
    const pipelineUrl = new URL(pipelineEndpoint);

    console.log(`=== Requesting pipeline at ${pipelineUrl} with body:\n`);
    console.log(PipelineDescription);
    console.log('');

    const response = await fetch(pipelineUrl, {
        method: "POST",
        headers: {
            "content-type": "text/turtle"
        },
        body: PipelineDescription,
    });

    if (response.status !== 200) {
        console.error(`Error: ${response.status}, response: ${await response.text()}`);
        return;
    }

    console.log(`= Status: ${response.status}\n`);
}

main();
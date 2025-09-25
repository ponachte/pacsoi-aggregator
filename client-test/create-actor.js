
import { URL } from 'url';

const ACTOR_NAME = 'observations';

const TOKEN_URL = 'https://kvasir-auth.faqir.org/realms/quarkus/protocol/openid-connect/token';
const CLIENT_ID = 'aggregator';
const CLIENT_SECRET = 'SubNkF1qZ0UkPmhr67YNOSLXNxF2mtuW';
const QUERY_ENDPOINT = 'https://kvasir.faqir.org/pol/slices/observations/query';

const SPARQL_QUERY = `
PREFIX moveUp: <http://moveUp.care/>
PREFIX xsd: <http://www.w3.org/2001/XMLSchema#>

SELECT ?subj ?date (AVG(?value) AS ?avgValue)
WHERE {
  ?obs moveUp:subject ?subj ;
       moveUp:valueQuantity ?valueQuantity ;
       moveUp:effectiveDateTime ?datetimeStr .
  ?valueQuantity moveUp:value ?value ;
                 moveUp:unit "steps per day" .

  BIND(xsd:dateTime(?datetimeStr) AS ?datetime)
  BIND(xsd:date(?datetime) AS ?date)
}
GROUP BY ?subj ?date
ORDER BY ?subj ?date
`

const SCHEMA_SOURCE = `type Query {
  observation(id: ID!): moveUp_Observation
  observations(cursor: String): [moveUp_Observation!]!
}

type moveUp_Procedure {
  id: ID!
  dct_description: String!
}

type moveUp_ValueQuantitySystem {
  moveUp_system: String!
  moveUp_code: String!
  moveUp_value: Float!
  moveUp_unit: String!
}

type moveUp_ValueCodeableConcept {
  dct_description: String!
  moveUp_coding(id: ID, cursor: String): [moveUp_Coding!]!
}

type moveUp_Code {
  moveUp_coding(id: ID, cursor: String): [moveUp_Coding!]!
}

type moveUp_Category {
  moveUp_coding(id: ID, cursor: String): [moveUp_Coding!]!
}

type moveUp_Coding {
  id: ID! 
  moveUp_system: ID!
  moveUp_code: String!
  dct_description: String!
}

type moveUp_Observation {
  id: ID!
  moveUp_status: String!
  moveUp_category(cursor: String): [moveUp_Category!]!
  moveUp_code: moveUp_Code!
  moveUp_subject: ID!
  moveUp_effectiveDateTime: String!
  moveUp_valueQuantity: moveUp_ValueQuantitySystem
  moveUp_valueCodeableConcept: moveUp_ValueCodeableConcept
  moveUp_partOf(id: ID, cursor: String): [moveUp_Procedure!]
}
`

const SCHEMA_CONTEXT = {
  "kss": "https://kvasir.discover.ilabt.imec.be/vocab#",
  "dct": "http://purl.org/dc/terms/",
  "xsd": "http://www.w3.org/2001/XMLSchema#",
  "r2r": "http://www4.wiwiss.fu-berlin.de/bizer/r2r/",
  "foaf": "http://xmlns.com/foaf/0.1/",
  "rdfs": "http://www.w3.org/2000/01/rdf-schema#",
  "rml": "http://w3id.org/rml/",
  "moveUp": "http://moveUp.care/"
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

    const body = {
      "name": ACTOR_NAME,
      "pipelineDescription": PipelineDescription
    }

    const response = await fetch(pipelineUrl, {
        method: "POST",
        headers: {
            "content-type": "application/json"
        },
        body: JSON.stringify(body),
    });

    if (response.status !== 200) {
        console.error(`Error: ${response.status}, response: ${await response.text()}`);
        return;
    }

    console.log(`= Status: ${response.status}\n`);
}

main();
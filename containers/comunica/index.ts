import { QueryEngine } from "@comunica-graphql/query-sparql-graphql";
import { Store, Parser } from 'n3';
import http from 'http';

const proxyUrl = process.env.http_proxy || process.env.HTTP_PROXY;
if (proxyUrl === undefined) {
  throw new Error('Environment variable PROXY_URL is not set. Please provide the URL of the proxy server.');
}

const fetchProxy: (input: RequestInfo | URL, init?: RequestInit) => Promise<Response> 
 = async (input: any, init?: any) => {
  
  // Prepare the request payload for the proxy
  const fetchRequest = {
    url: input.toString(),
    method: init?.method || 'GET',
    headers: init?.headers || {},
    body: init?.body ? init.body.toString() : ''
  };

  try {
    // Send request to proxy's /fetch endpoint and return the response directly
    const response = await fetch(`${proxyUrl}/fetch`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(fetchRequest)
    });

    // The proxy now returns the actual response, so we can return it directly
    return response;
  } catch (error) {
    console.error('Custom fetch error:', error);
    throw error;
  }
};

async function main() {
  const pipelineDescription = process.env.PIPELINE_DESCRIPTION;
  if (pipelineDescription === undefined) {
    throw new Error('Environment variable PIPELINE_DESCRIPTION is not set. Please provide a valid pipeline description.');
  }

  const queryEngine = new QueryEngine();
  const pipelineDescriptionStore = new Store();
  const pipelineParser = new Parser();

  // Parse pipeline description in n3 store
  await new Promise<void>(
    (resolve, reject) => {
      pipelineParser.parse(pipelineDescription, (error, quad, _prefixes) => {
        if (error) {
          reject('Error parsing pipeline description: ' + error);
          return;
        }
        if (quad) {
          pipelineDescriptionStore.addQuad(quad);
        } else {
          // Parsing finished;
          resolve();
        }
      });
    }
  );

  const pipelineStream = await queryEngine.queryBindings(`
    PREFIX fno: <https://w3id.org/function/ontology#>
    PREFIX config: <http://localhost:5000/config#>
    PREFIX rdf: <http://www.w3.org/1999/02/22-rdf-syntax-ns#>
    
    SELECT ?queryString ?url ?tokenUrl ?client ?secret ?schema ?context ?username ?password WHERE {
      ?exe a fno:Execution ;
        fno:executes config:SPARQLEvaluation ;
        config:sources ?sources ;
        config:queryString ?queryString ;
        config:tokenUrl ?tokenUrl ;
        config:client ?client ;
        config:secret ?secret ;
        config:username ?username ;
        config:password ?password .
      ?sources (rdf:rest*/rdf:first) ?source .
      ?source config:url ?url .
      OPTIONAL {
        ?source config:schema ?schema ;
          config:context ?context .
      }
    }`,
    { sources: [ pipelineDescriptionStore ] }
  );

  const queryInfo: QueryInfo = await new Promise<QueryInfo>(
    (resolve, reject) => {
      let queryString: string | undefined = undefined;
      let client: string | undefined = undefined;
      let secret: string | undefined = undefined;
      let username: string | undefined = undefined;
      let password: string | undefined = undefined;
      let tokenUrl: string | undefined = undefined;
      let sources: [Source, ...Source[]] | undefined = undefined;

      pipelineStream.on('data', (data) => {
        if (queryString === undefined && data.get('queryString').value !== undefined) {
          queryString = data.get('queryString').value;
        }
        if (client === undefined && data.get('client').value !== undefined) {
          client = data.get('client').value;
        }
        if (secret === undefined && data.get('secret').value !== undefined) {
          secret = data.get('secret').value;
        }
        if (username === undefined && data.get('username').value !== undefined) {
          username = data.get('username').value;
        }
        if (password === undefined && data.get('password').value !== undefined) {
          password = data.get('password').value;
        }
        if (tokenUrl === undefined && data.get('tokenUrl').value !== undefined) {
          tokenUrl = data.get('tokenUrl').value;
        }

        if (data.get('queryString').value === queryString) {
          const source: Source = {
            type: 'graphql',
            value: data.get('url').value
          }
          if (data.get('schema').value !== undefined) {
            source.context = {
              schema: data.get('schema').value,
              context: JSON.parse(data.get('context').value),
            }
          }

          if (sources === undefined) {
            sources = [source];
            return;
          }
          sources.push(source);
        }
      });

      pipelineStream.on('end', () => {
        if (queryString === undefined) {
          reject(new Error('No query string found in the pipeline description.'));
          return;
        }
        if (client === undefined) {
          reject(new Error('No client found in the pipeline description.'));
          return;
        }
        if (secret === undefined) {
          reject(new Error('No secret found in the pipeline description.'));
          return;
        }
        if (username === undefined) {
          reject(new Error('No secret found in the pipeline description.'));
          return;
        }
        if (password === undefined) {
          reject(new Error('No secret found in the pipeline description.'));
          return;
        }
        if (tokenUrl === undefined) {
          reject(new Error('No tokenUrl found in the pipeline description.'));
          return;
        }
        if (sources === undefined) {
          reject(new Error('No sources found in the pipeline description.'));
          return;
        }

        resolve({ 
          query: queryString,
          client,
          secret,
          tokenUrl,
          username,
          password, 
          sources, 
        });
      });

      pipelineStream.on('error', (error) => reject(error));
    }
  );
  pipelineStream.destroy();

  console.log(`Executing SPARQL SELECT query: ${queryInfo.query}`);
  console.log(`Using sources: ${JSON.stringify(queryInfo.sources)}`);
  console.log(`Using client: ${queryInfo.client}`);
  console.log(`Using secret: ${queryInfo.secret}`);
  console.log(`Using tokenUrl: ${queryInfo.tokenUrl}`);
  console.log(`Using username: ${queryInfo.username}`);
  console.log(`Using password: ${queryInfo.password}`);

  // Authorization
  const getAccessToken = async () => {
    const params = new URLSearchParams({
      grant_type: 'password',
      username: queryInfo.username,
      password: queryInfo.password,
      client_id: queryInfo.client,
      client_secret: queryInfo.secret,
    });

    const response = await fetch(queryInfo.tokenUrl, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/x-www-form-urlencoded'
      },
      body: params.toString()
    });

    if (!response.ok) {
      const errorText = await response.text();
      throw new Error(`Token request failed: ${response.status} ${errorText}`);
    }

    const data = await response.json();
    if (!data.access_token) {
      throw new Error('Token not found in response.');
    }

    return data.access_token;
  };

  const server = http.createServer(async (req, res) => {
    console.log(`Received request: ${req.method} ${req.url}`);

    if (req.method === "GET" && req.url === "/") {
      res.writeHead(200, { "Content-Type": "application/sparql-results+json" });

      const token = await getAccessToken();

      console.log("recieved access token: ", token);
      
      const result = await queryEngine.query(queryInfo.query, { 
        sources: queryInfo.sources,
        fetch: (input: URL | RequestInfo, init: RequestInit = {}) => {
          if (!init.headers) {
            init.headers = {};
          }

          const headers = new Headers(init.headers);
          headers.append('Authorization', `Bearer ${token}`);
          init.headers = headers;

          return fetch(input, init);
        }
      });

      if (result.resultType !== 'bindings') {
        res.writeHead(400, { "Content-Type": "text/plain" });
        res.end("Only SELECT queries with bindings are supported.");
        return;
      }

      const { data } = await queryEngine.resultToString(result, "application/sparql-results+json");
      data.pipe(res)
    } else {
      res.writeHead(404, { "Content-Type": "text/plain" });
      res.end("Not found");
    }
  });

  server.listen(8080, () => {
    console.log("SPARQL SELECT result server running at http://localhost:8080/");
  });
}

interface QueryInfo {
  query: string,
  sources: [Source, ...Source[]],
  client: string,
  secret: string,
  tokenUrl: string,
  username: string,
  password: string
}

interface Source {
  type: 'graphql',
  value: string,
  context?: {
    schema: string,
    context: Record<string, string>,
  },
}

main();
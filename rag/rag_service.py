"""
RAG module for Order Processing Pipeline
Handles:
- OpenBao secret retrieval
- HuggingFace embeddings (nomic-ai/nomic-embed-text-v1.5)
- Pinecone vector store
- Merchant items retrieval and search
"""

import os
import logging
from typing import List, Dict, Any, Optional
import hvac
from langchain_community.embeddings import HuggingFaceEmbeddings
from langchain_pinecone import PineconeVectorStore
from langchain_core.documents import Document
from pinecone import Pinecone, ServerlessSpec

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)


class OpenBaoClient:
    """Client for fetching secrets from OpenBao"""
    
    def __init__(self):
        self.vault_addr = os.getenv("VAULT_ADDR", "http://localhost:8200")
        self.role_id = os.getenv("BAO_ROLE_ID")
        self.secret_id = os.getenv("BAO_SECRET_ID")
        self.client = None
        
    def authenticate(self):
        """Authenticate with OpenBao using AppRole"""
        try:
            self.client = hvac.Client(url=self.vault_addr)
            
            if not self.role_id or not self.secret_id:
                raise ValueError("BAO_ROLE_ID and BAO_SECRET_ID must be set")
            
            response = self.client.auth.approle.login(
                role_id=self.role_id,
                secret_id=self.secret_id
            )
            
            if not response or 'auth' not in response:
                raise Exception("AppRole authentication failed")
            
            self.client.token = response['auth']['client_token']
            logger.info("✓ Successfully authenticated with OpenBao")
            return True
            
        except Exception as e:
            logger.error(f"Failed to authenticate with OpenBao: {e}")
            raise
    
    def get_secret(self, path: str, key: str) -> str:
        """
        Fetch a secret from OpenBao
        
        Args:
            path: Secret path (e.g., "secret/data/myapp/pinecone")
            key: Key within the secret
            
        Returns:
            Secret value as string
        """
        if not self.client:
            self.authenticate()
        
        try:
            # Read secret from OpenBao
            secret = self.client.secrets.kv.v2.read_secret_version(
                path=path.replace("secret/data/", "").replace("secret/", ""),
                mount_point="secret"
            )
            
            if not secret or 'data' not in secret or 'data' not in secret['data']:
                raise Exception(f"No data found at path {path}")
            
            data = secret['data']['data']
            
            if key not in data:
                raise Exception(f"Key {key} not found in secret at {path}")
            
            return data[key]
            
        except Exception as e:
            logger.error(f"Failed to fetch secret from {path}/{key}: {e}")
            raise
    
    def get_secrets_dict(self, path: str) -> Dict[str, str]:
        """
        Fetch all secrets from a path as a dictionary
        
        Args:
            path: Secret path (e.g., "secret/data/myapp/pinecone")
            
        Returns:
            Dictionary of all secrets at that path
        """
        if not self.client:
            self.authenticate()
        
        try:
            secret = self.client.secrets.kv.v2.read_secret_version(
                path=path.replace("secret/data/", "").replace("secret/", ""),
                mount_point="secret"
            )
            
            if not secret or 'data' not in secret or 'data' not in secret['data']:
                raise Exception(f"No data found at path {path}")
            
            return secret['data']['data']
            
        except Exception as e:
            logger.error(f"Failed to fetch secrets from {path}: {e}")
            raise


class RAGService:
    """
    RAG service for merchant items search using:
    - HuggingFace embeddings (nomic-ai/nomic-embed-text-v1.5)
    - Pinecone vector store
    - OpenBao for secrets
    """
    
    def __init__(self):
        self.bao_client = OpenBaoClient()
        self.embeddings = None
        self.pinecone_client = None
        self.index = None
        self.vectorstore = None
        self.index_name = "merchant-items"
        self.namespace = ""  # Default namespace (empty string is the default namespace in Pinecone)
        self._initialized = False
        
    def initialize(self):
        """Initialize RAG service with secrets from OpenBao"""
        if self._initialized:
            return
        
        logger.info("Initializing RAG service...")
        
        try:
            # Authenticate with OpenBao
            self.bao_client.authenticate()
            
            # Fetch Pinecone secrets
            pinecone_secrets = self.bao_client.get_secrets_dict("secret/data/myapp/pinecone")
            pinecone_api_key = pinecone_secrets.get("PINECONE_API_KEY")
            pinecone_environment = pinecone_secrets.get("PINECONE_ENVIRONMENT", "us-east-1")
            
            if not pinecone_api_key:
                raise ValueError("PINECONE_API_KEY not found in OpenBao")
            
            # Initialize HuggingFace embeddings
            logger.info("Loading HuggingFace embedding model: nomic-ai/nomic-embed-text-v1.5")
            self.embeddings = HuggingFaceEmbeddings(
                model_name="nomic-ai/nomic-embed-text-v1.5",
                model_kwargs={'device': 'cpu', 'trust_remote_code': True},
                encode_kwargs={'normalize_embeddings': True}
            )
            
            # Initialize Pinecone
            logger.info(f"Connecting to Pinecone (environment: {pinecone_environment})")
            self.pinecone_client = Pinecone(api_key=pinecone_api_key)
            
            # Check if index exists, create if not
            existing_indexes = [idx.name for idx in self.pinecone_client.list_indexes()]
            
            if self.index_name not in existing_indexes:
                logger.info(f"Creating Pinecone index: {self.index_name}")
                self.pinecone_client.create_index(
                    name=self.index_name,
                    dimension=768,  # nomic-embed-text-v1.5 dimension
                    metric="cosine",
                    spec=ServerlessSpec(
                        cloud="aws",
                        region=pinecone_environment
                    )
                )
                logger.info(f"✓ Created Pinecone index: {self.index_name}")
            else:
                logger.info(f"✓ Using existing Pinecone index: {self.index_name}")
            
            # Initialize Langchain Pinecone vectorstore
            # Use langchain-pinecone which handles the Pinecone client properly
            self.vectorstore = PineconeVectorStore(
                index_name=self.index_name,
                embedding=self.embeddings,
                pinecone_api_key=pinecone_api_key,
                text_key="text"
            )
            
            # Store index reference for direct operations if needed
            self.index = self.pinecone_client.Index(self.index_name)
            
            self._initialized = True
            logger.info("✓ RAG service initialized successfully")
            
        except Exception as e:
            logger.error(f"Failed to initialize RAG service: {e}")
            raise
    
    def add_documents(self, documents: List[Document]):
        """
        Add documents to the Pinecone index
        
        Args:
            documents: List of Langchain Document objects
        """
        if not self._initialized:
            self.initialize()
        
        try:
            logger.info(f"Adding {len(documents)} documents to Pinecone index")
            self.vectorstore.add_documents(documents)
            logger.info(f"✓ Successfully added {len(documents)} documents")
        except Exception as e:
            logger.error(f"Failed to add documents: {e}")
            raise
    
    def similarity_search(
        self, 
        query: str, 
        k: int = 5,
        filter: Optional[Dict[str, Any]] = None
    ) -> List[Document]:
        """
        Search for similar documents
        
        Args:
            query: Search query
            k: Number of results to return
            filter: Optional metadata filter
            
        Returns:
            List of similar documents
        """
        if not self._initialized:
            self.initialize()
        
        try:
            logger.info(f"Searching for: {query} (top {k} results)")
            results = self.vectorstore.similarity_search(
                query=query,
                k=k,
                filter=filter
            )
            logger.info(f"✓ Found {len(results)} results")
            return results
        except Exception as e:
            logger.error(f"Search failed: {e}")
            raise
    
    def similarity_search_with_score(
        self,
        query: str,
        k: int = 5,
        filter: Optional[Dict[str, Any]] = None
    ) -> List[tuple[Document, float]]:
        """
        Search for similar documents with relevance scores
        
        Args:
            query: Search query
            k: Number of results to return
            filter: Optional metadata filter
            
        Returns:
            List of tuples (document, score)
        """
        if not self._initialized:
            self.initialize()
        
        try:
            logger.info(f"Searching with scores for: {query} (top {k} results)")
            results = self.vectorstore.similarity_search_with_score(
                query=query,
                k=k,
                filter=filter
            )
            logger.info(f"✓ Found {len(results)} results with scores")
            return results
        except Exception as e:
            logger.error(f"Search with score failed: {e}")
            raise
    
    def get_database_connection_string(self) -> str:
        """
        Get PostgreSQL connection string from OpenBao or environment
        Priority: DATABASE_URL from OpenBao > construct from password + env vars
        
        Returns:
            Database connection string
        """
        try:
            db_secrets = self.bao_client.get_secrets_dict("secret/data/myapp/database")
            
            # First try to get complete DATABASE_URL
            if "DATABASE_URL" in db_secrets:
                logger.info("✓ Using DATABASE_URL from OpenBao")
                return db_secrets["DATABASE_URL"]
            
            # Otherwise, get password from OpenBao and other params from env
            password = db_secrets.get("ORDER_DB_PASSWORD")
            if not password:
                raise ValueError("ORDER_DB_PASSWORD not found in OpenBao")
            
            # Get other parameters from environment variables
            host = os.getenv("ORDER_DB_HOST", "localhost")
            port = os.getenv("ORDER_DB_PORT", "5432")
            database = os.getenv("ORDER_DB_NAME", "orderpipeline")
            user = os.getenv("ORDER_DB_USER", "orderpipelineadmin")
            
            conn_string = f"postgresql://{user}:{password}@{host}:{port}/{database}"
            logger.info("✓ Built database connection string from OpenBao password + env vars")
            return conn_string
            
        except Exception as e:
            logger.error(f"Failed to get database connection string: {e}")
            raise
    
    def get_openrouter_api_key(self) -> str:
        """
        Get OpenRouter API key from OpenBao
        
        Returns:
            OpenRouter API key
        """
        try:
            api_key = self.bao_client.get_secret("secret/data/myapp/openrouter", "OPENROUTER_API_KEY")
            logger.info("✓ Retrieved OpenRouter API key from OpenBao")
            return api_key
        except Exception as e:
            logger.error(f"Failed to get OpenRouter API key: {e}")
            raise
    
    def clear_index(self):
        """Clear all vectors from the Pinecone index"""
        if not self._initialized:
            self.initialize()
        
        try:
            logger.info(f"Clearing Pinecone index: {self.index_name}")
            # Check if namespace exists by getting stats
            stats = self.index.describe_index_stats()
            namespace_stats = stats.get('namespaces', {})
            
            if self.namespace in namespace_stats:
                self.index.delete(delete_all=True, namespace=self.namespace)
                logger.info(f"✓ Index namespace '{self.namespace}' cleared successfully")
            else:
                logger.info(f"✓ Namespace '{self.namespace}' doesn't exist yet, nothing to clear")
        except Exception as e:
            # If it's a 404 error for namespace not found, that's okay
            if "404" in str(e) or "Namespace not found" in str(e):
                logger.info(f"✓ Namespace '{self.namespace}' doesn't exist yet, nothing to clear")
            else:
                logger.error(f"Failed to clear index: {e}")
                raise


# Global RAG service instance
_rag_service: Optional[RAGService] = None


def get_rag_service() -> RAGService:
    """Get or create the global RAG service instance"""
    global _rag_service
    if _rag_service is None:
        _rag_service = RAGService()
    return _rag_service

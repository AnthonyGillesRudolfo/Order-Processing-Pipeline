"""
RAG (Retrieval-Augmented Generation) module for Order Processing Pipeline

This module provides semantic search capabilities for merchant items using:
- Langchain for document processing
- Pinecone for vector storage
- HuggingFace embeddings (nomic-ai/nomic-embed-text-v1.5)
- OpenBao for secure secret management

Main components:
- rag_service.py: Core RAG functionality
- ingest_data.py: Data ingestion from PostgreSQL to Pinecone
"""

from .rag_service import RAGService, get_rag_service, OpenBaoClient

__all__ = [
    'RAGService',
    'get_rag_service',
    'OpenBaoClient',
]

__version__ = '0.1.0'

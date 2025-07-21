import React, { useState } from 'react';
import axios from 'axios';

// Parse DNS recordData based on recordType
const parseRecordValue = (recordData, recordType) => {
  try {
    // Normalize tabs to spaces and split on whitespace
    const parts = recordData.trim().replace(/\t/g, ' ').split(/\s+/);
    switch (recordType) {
      case 'A':
      case 'AAAA':
        return parts[parts.length - 1]; // IP address
      case 'MX':
        return parts.slice(-2).join(' '); // Priority and mail server
      case 'TXT':
        return parts.slice(4).join(' ').replace(/"/g, ''); // Text data, remove quotes
      case 'CNAME':
        return parts[parts.length - 1]; // Canonical name
      default:
        return recordData; // Fallback to raw data
    }
  } catch (e) {
    console.error('Error parsing recordData:', e);
    return recordData; // Fallback to raw data
  }
};

const App = () => {
  const [domain, setDomain] = useState('');
  const [recordTypes, setRecordTypes] = useState([]);
  const [records, setRecords] = useState([]);
  const [error, setError] = useState('');
  const [apiKey, setApiKey] = useState('550e8400-e29b-41d4-a716-446655440000'); // Replace with actual API key

  const recordTypeOptions = ['A', 'AAAA', 'MX', 'TXT', 'CNAME'];

  const handleSearch = async (e) => {
    e.preventDefault();
    setError('');
    setRecords([]);
    try {
      const response = await axios.get(`http://34.21.17.237:8080/v1/records/${domain}`, {
        headers: { 'X-API-Key': apiKey },
        params: { record_type: recordTypes },
        paramsSerializer: params => {
          return Object.keys(params)
              .map(key => params[key].map(v => `${key}=${encodeURIComponent(v)}`).join('&'))
              .join('&');
        },
      });
      console.log('API Response:', response.data); // Log response for debugging
      if (!response.data.records) {
        setError('No records found in response');
        return;
      }
      setRecords(response.data.records);
    } catch (err) {
      console.error('API Error:', err.response?.data || err.message);
      setError(err.response?.data?.message || 'Failed to fetch records');
    }
  };

  const handleRecordTypeChange = (e) => {
    const value = e.target.value;
    setRecordTypes(
        e.target.checked
            ? [...recordTypes, value]
            : recordTypes.filter(type => type !== value)
    );
  };

  return (
      <div className="container mx-auto p-4">
        <h1 className="text-2xl font-bold mb-4">DNS Records Search</h1>
        <form onSubmit={handleSearch} className="mb-4">
          <div className="mb-4">
            <label className="block text-sm font-medium">Domain Name</label>
            <input
                type="text"
                value={domain}
                onChange={(e) => setDomain(e.target.value.trim())}
                className="mt-1 block w-full border rounded p-2"
                placeholder="917182.baby"
                required
            />
          </div>
          <div className="mb-4">
            <label className="block text-sm font-medium">Record Types</label>
            {recordTypeOptions.map(type => (
                <div key={type} className="flex items-center">
                  <input
                      type="checkbox"
                      value={type}
                      checked={recordTypes.includes(type)}
                      onChange={handleRecordTypeChange}
                      className="mr-2"
                  />
                  <label>{type}</label>
                </div>
            ))}
          </div>
          <button type="submit" className="bg-blue-500 text-white px-4 py-2 rounded">
            Search
          </button>
        </form>
        {error && <p className="text-red-500">{error}</p>}
        {records.length > 0 ? (
            <table className="w-full border-collapse border">
              <thead>
              <tr className="bg-gray-200">
                <th className="border p-2">Domain ID</th>
                <th className="border p-2">Record Type</th>
                <th className="border p-2">Value</th>
                <th className="border p-2">TTL</th>
                <th className="border p-2">Source</th>
                <th className="border p-2">Last Updated</th>
                <th className="border p-2">Raw Data</th>
              </tr>
              </thead>
              <tbody>
              {records.map((record, index) => {
                console.log('Rendering record:', record); // Log each record for debugging
                return (
                    <tr key={index} className="even:bg-gray-50">
                      <td className="border p-2">{record.domainId || 'N/A'}</td>
                      <td className="border p-2">{record.recordType || 'N/A'}</td>
                      <td className="border p-2">{parseRecordValue(record.recordData || '', record.recordType) || 'N/A'}</td>
                      <td className="border p-2">{record.ttl !== undefined ? record.ttl : 'N/A'}</td>
                      <td className="border p-2">{record.source || 'N/A'}</td>
                      <td className="border p-2">{record.lastUpdated || 'N/A'}</td>
                      <td className="border p-2">{record.recordData || 'N/A'}</td>
                    </tr>
                );
              })}
              </tbody>
            </table>
        ) : (
            <p>No records found.</p>
        )}
      </div>
  );
};

export default App;
import { Routes, Route, Navigate } from 'react-router-dom'
import Layout from './components/Layout'
import Creators from './pages/Creators'
import CreatorDetail from './pages/CreatorDetail'
import VideoPage from './pages/VideoPage'
import Groups from './pages/Groups'
import GroupFeed from './pages/GroupFeed'
import Search from './pages/Search'

export default function App() {
    return (
        <Layout>
            <Routes>
                <Route path="/" element={<Navigate to="/creators" replace />} />
                <Route path="/creators" element={<Creators />} />
                <Route path="/creators/:id" element={<CreatorDetail />} />
                <Route path="/videos/:id" element={<VideoPage />} />
                <Route path="/groups" element={<Groups />} />
                <Route path="/groups/:slug" element={<GroupFeed />} />
                <Route path="/search" element={<Search />} />
                <Route path="*" element={<div className="panel">Not found.</div>} />
            </Routes>
        </Layout>
    )
}
